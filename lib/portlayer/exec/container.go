// Copyright 2016 VMware, Inc. All Rights Reserved.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//    http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package exec

import (
	"context"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/vmware/govmomi/object"
	"github.com/vmware/govmomi/vim25/mo"
	"github.com/vmware/govmomi/vim25/soap"
	"github.com/vmware/govmomi/vim25/types"
	"github.com/vmware/vic/pkg/errors"
	"github.com/vmware/vic/pkg/trace"
	"github.com/vmware/vic/pkg/uid"
	"github.com/vmware/vic/pkg/vsphere/session"
	"github.com/vmware/vic/pkg/vsphere/sys"
	"github.com/vmware/vic/pkg/vsphere/tasks"
	"github.com/vmware/vic/pkg/vsphere/vm"

	log "github.com/Sirupsen/logrus"
	"github.com/google/uuid"
)

type State int

const (
	StateUnknown State = iota
	StateStarting
	StateRunning
	StateStopping
	StateStopped
	StateSuspending
	StateSuspended
	StateCreated
	StateCreating
	StateRemoving
	StateRemoved

	propertyCollectorTimeout = 3 * time.Minute
	containerLogName         = "output.log"

	vmNotSuspendedKey = "msg.suspend.powerOff.notsuspended"
)

func (s State) String() string {
	switch s {
	case StateCreated:
		return "Created"
	case StateStarting:
		return "Starting"
	case StateRunning:
		return "Running"
	case StateRemoving:
		return "Removing"
	case StateRemoved:
		return "Removed"
	case StateStopping:
		return "Stopping"
	case StateStopped:
		return "Stopped"
	case StateUnknown:
		return "Unknown"
	}
	return ""
}

// NotFoundError is returned when a types.ManagedObjectNotFound is returned from a vmomi call
type NotFoundError struct {
	err error
}

func (r NotFoundError) Error() string {
	return "VM has either been deleted or has not been fully created"
}

// RemovePowerError is returned when attempting to remove a containerVM that is powered on
type RemovePowerError struct {
	err error
}

func (r RemovePowerError) Error() string {
	return r.err.Error()
}

// ConcurrentAccessError is returned when concurrent calls tries to modify same object
type ConcurrentAccessError struct {
	err error
}

func (r ConcurrentAccessError) Error() string {
	return r.err.Error()
}

// Container is used to return data about a container during inspection calls
// It is a copy rather than a live reflection and does not require locking
type ContainerInfo struct {
	containerBase

	state State

	// Size of the leaf (unused)
	VMUnsharedDisk int64
}

// Container is used for an entry in the container cache - this is a "live" representation
// of containers in the infrastructure.
// DANGEROUS USAGE CONSTRAINTS:
//   None of the containerBase fields should be partially updated - consider them immutable once they're
//   part of a cache entry
//   i.e. Do not make changes in containerBase.ExecConfig - only swap, under lock, the pointer for a
//   completely new ExecConfig.
//   This constraint allows us to avoid deep copying those structs every time a container is inspected
type Container struct {
	m sync.Mutex

	ContainerInfo

	logFollowers []io.Closer

	newStateEvents map[State]chan struct{}
}

// newContainer constructs a Container suitable for adding to the cache
// it's state is set from the Runtime.PowerState field, or StateCreated if that is not
// viable
// This copies (shallow) the containerBase that's provided
func newContainer(base *containerBase) *Container {
	c := &Container{
		ContainerInfo: ContainerInfo{
			containerBase: *base,
			state:         StateCreated,
		},
		newStateEvents: make(map[State]chan struct{}),
	}

	// if this is a creation path, then Runtime will be nil
	if base.Runtime != nil {
		// set state
		switch base.Runtime.PowerState {
		case types.VirtualMachinePowerStatePoweredOn:
			c.state = StateRunning
		case types.VirtualMachinePowerStatePoweredOff:
			// check if any of the sessions was started
			for _, s := range base.ExecConfig.Sessions {
				if s.Started != "" {
					c.state = StateStopped
					break
				}
			}
		case types.VirtualMachinePowerStateSuspended:
			c.state = StateSuspended
			log.Warnf("container VM %s: invalid power state %s", base.vm.Reference(), base.Runtime.PowerState)
		}
	}

	return c
}

func GetContainer(ctx context.Context, id uid.UID) *Handle {
	// get from the cache
	container := Containers.Container(id.String())
	if container != nil {
		return container.NewHandle(ctx)
	}

	return nil
}

// State returns the state at the time the ContainerInfo object was created
func (c *ContainerInfo) State() State {
	return c.state
}

// Info returns a copy of the public container configuration that
// is consistent and copied under lock
func (c *Container) Info() *ContainerInfo {
	c.m.Lock()
	defer c.m.Unlock()

	info := c.ContainerInfo
	return &info
}

// CurrentState returns current state.
func (c *Container) CurrentState() State {
	c.m.Lock()
	defer c.m.Unlock()
	return c.state
}

// SetState changes container state.
func (c *Container) SetState(s State) State {
	c.m.Lock()
	defer c.m.Unlock()
	return c.updateState(s)
}

func (c *Container) updateState(s State) State {
	log.Debugf("Setting container %s state: %s", c.ExecConfig.ID, s)
	prevState := c.state
	if s != c.state {
		c.state = s
		if ch, ok := c.newStateEvents[s]; ok {
			delete(c.newStateEvents, s)
			close(ch)
		}
	}
	return prevState
}

var closedEventChannel = func() <-chan struct{} {
	a := make(chan struct{})
	close(a)
	return a
}()

// WaitForState subscribes a caller to an event returning
// a channel that will be closed when an expected state is set.
// If expected state is already set the caller will receive a closed channel immediately.
func (c *Container) WaitForState(s State) <-chan struct{} {
	c.m.Lock()
	defer c.m.Unlock()

	if s == c.state {
		return closedEventChannel
	}

	if ch, ok := c.newStateEvents[s]; ok {
		return ch
	}

	eventChan := make(chan struct{})
	c.newStateEvents[s] = eventChan
	return eventChan
}

func (c *Container) NewHandle(ctx context.Context) *Handle {
	// Call property collector to fill the data
	if c.vm != nil {
		// FIXME: this should be calling the cache to decide if a refresh is needed
		if err := c.Refresh(ctx); err != nil {
			log.Errorf("refreshing container %s failed: %s", c.ExecConfig.ID, err)
			return nil // nil indicates error
		}
	}

	// return a handle that represents zero changes over the current configuration
	// for this container
	return newHandle(c)
}

// Refresh updates config and runtime info, holding a lock only while swapping
// the new data for the old
func (c *Container) Refresh(ctx context.Context) error {
	defer trace.End(trace.Begin(c.ExecConfig.ID))

	base, err := c.updates(ctx)
	if err != nil {
		log.Errorf("Unable to update container %s", c.ExecConfig.ID)
		return err
	}

	c.m.Lock()
	defer c.m.Unlock()

	// copy over the new state
	c.containerBase = *base
	return nil
}

// Refresh updates config and runtime info, holding a lock only while swapping
// the new data for the old
func (c *Container) RefreshFromHandle(ctx context.Context, h *Handle) {
	defer trace.End(trace.Begin(h.String()))

	c.m.Lock()
	defer c.m.Unlock()

	if c.Config != nil && (h.Config == nil || h.Config.ChangeVersion != c.Config.ChangeVersion) {
		log.Warnf("container and handle ChangeVersions do not match: %s != %s", c.Config.ChangeVersion, h.Config.ChangeVersion)
		return
	}

	// copy over the new state
	c.containerBase = h.containerBase
	log.Debugf("container refreshed - ChangeVersion: %s", c.Config.ChangeVersion)
}

// Start starts a container vm with the given params
func (c *Container) start(ctx context.Context) error {
	defer trace.End(trace.Begin(c.ExecConfig.ID))

	if c.vm == nil {
		return fmt.Errorf("vm not set")
	}
	// get existing state and set to starting
	// if there's a failure we'll revert to existing
	finalState := c.updateState(StateStarting)
	defer func() { c.updateState(finalState) }()

	err := c.containerBase.start(ctx)
	if err != nil {
		// leave this in state starting - if it powers off then the event
		// will cause transition to StateStopped which is likely our original state
		// if the container was just taking a very long time it'll eventually
		// become responsive.

		// TODO: mechanism to trigger reinspection of long term transitional states
		finalState = StateStarting
		return err
	}

	finalState = StateRunning

	return err
}

func (c *Container) stop(ctx context.Context, waitTime *int32) error {
	defer trace.End(trace.Begin(c.ExecConfig.ID))

	defer c.onStop()

	// get existing state and set to stopping
	// if there's a failure we'll revert to existing
	finalState := c.updateState(StateStopping)
	defer func() { c.updateState(finalState) }()

	err := c.containerBase.stop(ctx, waitTime)

	if err != nil {
		// we've got no idea what state the container is in at this point
		// running is an _optimistic_ statement
		return err
	}

	finalState = StateStopped
	return nil
}

func (c *Container) Signal(ctx context.Context, num int64) error {
	defer trace.End(trace.Begin(c.ExecConfig.ID))

	if c.vm == nil {
		return fmt.Errorf("vm not set")
	}

	return c.startGuestProgram(ctx, "kill", fmt.Sprintf("%d", num))
}

func (c *Container) onStop() {
	lf := c.logFollowers
	c.logFollowers = nil

	log.Debugf("Container(%s) closing %d log followers", c.ExecConfig.ID, len(lf))
	for _, l := range lf {
		_ = l.Close()
	}
}

func (c *Container) LogReader(ctx context.Context, tail int, follow bool) (io.ReadCloser, error) {
	defer trace.End(trace.Begin(c.ExecConfig.ID))
	c.m.Lock()
	defer c.m.Unlock()

	if c.vm == nil {
		return nil, fmt.Errorf("vm not set")
	}

	url, err := c.vm.DSPath(ctx)
	if err != nil {
		return nil, err
	}

	name := fmt.Sprintf("%s/%s", url.Path, containerLogName)

	log.Infof("pulling %s", name)

	file, err := c.vm.Datastore.Open(ctx, name)
	if err != nil {
		return nil, err
	}

	if tail >= 0 {
		err = file.Tail(tail)
		if err != nil {
			return nil, err
		}
	}

	if follow && c.state == StateRunning {
		follower := file.Follow(time.Second)

		c.logFollowers = append(c.logFollowers, follower)

		return follower, nil
	}

	return file, nil
}

// Remove removes a containerVM after detaching the disks
func (c *Container) Remove(ctx context.Context, sess *session.Session) error {
	defer trace.End(trace.Begin(c.ExecConfig.ID))
	c.m.Lock()
	defer c.m.Unlock()

	if c.vm == nil {
		return NotFoundError{}
	}

	// check state first
	if c.state == StateRunning {
		return RemovePowerError{fmt.Errorf("Container is powered on")}
	}

	// get existing state and set to removing
	// if there's a failure we'll revert to existing
	existingState := c.updateState(StateRemoving)

	// get the folder the VM is in
	url, err := c.vm.DSPath(ctx)
	if err != nil {

		// handle the out-of-band removal case
		if soap.IsSoapFault(err) {
			fault := soap.ToSoapFault(err).VimFault()
			if _, ok := fault.(types.ManagedObjectNotFound); ok {
				Containers.Remove(c.ExecConfig.ID)
				return NotFoundError{}
			}
		}

		log.Errorf("Failed to get datastore path for %s: %s", c.ExecConfig.ID, err)
		c.updateState(existingState)
		return err
	}
	// FIXME: was expecting to find a utility function to convert to/from datastore/url given
	// how widely it's used but couldn't - will ask around.
	dsPath := fmt.Sprintf("[%s] %s", url.Host, url.Path)

	//removes the vm from vsphere, but detaches the disks first
	_, err = c.vm.WaitForResult(ctx, func(ctx context.Context) (tasks.Task, error) {
		return c.vm.DeleteExceptDisks(ctx)
	})
	if err != nil {
		f, ok := err.(types.HasFault)
		if !ok {
			c.updateState(existingState)
			return err
		}
		switch f.Fault().(type) {
		case *types.InvalidState:
			log.Warnf("container VM is in invalid state, unregistering")
			if err := c.vm.Unregister(ctx); err != nil {
				log.Errorf("Error while attempting to unregister container VM: %s", err)
				return err
			}
		default:
			log.Debugf("Fault while attempting to destroy vm: %#v", f.Fault())
			c.updateState(existingState)
			return err
		}
	}

	// remove from datastore
	fm := object.NewFileManager(c.vm.Client.Client)

	if _, err = tasks.WaitForResult(ctx, func(ctx context.Context) (tasks.Task, error) {
		return fm.DeleteDatastoreFile(ctx, dsPath, sess.Datacenter)
	}); err != nil {
		// at this phase error doesn't matter. Just log it.
		log.Debugf("Failed to delete %s, %s", dsPath, err)
	}

	//remove container from cache
	Containers.Remove(c.ExecConfig.ID)
	return nil
}

// get the containerVMs from infrastructure for this resource pool
func infraContainers(ctx context.Context, sess *session.Session) ([]*Container, error) {
	defer trace.End(trace.Begin(""))
	var rp mo.ResourcePool

	// popluate the vm property of the vch resource pool
	if err := Config.ResourcePool.Properties(ctx, Config.ResourcePool.Reference(), []string{"vm"}, &rp); err != nil {
		name := Config.ResourcePool.Name()
		log.Errorf("List failed to get %s resource pool child vms: %s", name, err)
		return nil, err
	}
	vms, err := populateVMAttributes(ctx, sess, rp.Vm)
	if err != nil {
		return nil, err
	}

	return convertInfraContainers(ctx, sess, vms), nil
}

func instanceUUID(id string) (string, error) {
	// generate VM instance uuid, which will be used to query back VM
	u, err := sys.UUID()
	if err != nil {
		return "", err
	}
	namespace, err := uuid.Parse(u)
	if err != nil {
		return "", errors.Errorf("unable to parse VCH uuid: %s", err)
	}
	return uuid.NewSHA1(namespace, []byte(id)).String(), nil
}

// populate the vm attributes for the specified morefs
func populateVMAttributes(ctx context.Context, sess *session.Session, refs []types.ManagedObjectReference) ([]mo.VirtualMachine, error) {
	defer trace.End(trace.Begin(fmt.Sprintf("populating %d refs", len(refs))))
	var vms []mo.VirtualMachine

	// current attributes we care about
	attrib := []string{"config", "runtime.powerState", "summary"}

	// populate the vm properties
	err := sess.Retrieve(ctx, refs, attrib, &vms)
	return vms, err
}

// convert the infra containers to a container object
func convertInfraContainers(ctx context.Context, sess *session.Session, vms []mo.VirtualMachine) []*Container {
	defer trace.End(trace.Begin(fmt.Sprintf("converting %d containers", len(vms))))
	var cons []*Container

	for _, v := range vms {
		vm := vm.NewVirtualMachine(ctx, sess, v.Reference())
		base := newBase(vm, v.Config, &v.Runtime)
		c := newContainer(base)

		id := uid.Parse(c.ExecConfig.ID)
		if id == uid.NilUID {
			log.Warnf("skipping converting container VM %s: could not parse id", v.Reference())
			continue
		}

		if v.Summary.Storage != nil {
			c.VMUnsharedDisk = v.Summary.Storage.Unshared
		}

		cons = append(cons, c)
	}

	return cons
}
