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

// Package session caches vSphere objects to avoid having to repeatedly
// make govmomi client calls.
//
// To obtain a Session, call Create with a Config. The config
// contains the SDK URL (Service) and the desired vSphere resources.
// Create then connects to Service and stores govmomi objects for
// each corresponding value in Config. The Session is returned and
// the user can use the cached govmomi objects in the exported fields of
// Session instead of directly using a govmomi Client.
//
package session

import (
	"crypto/tls"
	"fmt"
	"strings"
	"time"

	log "github.com/Sirupsen/logrus"
	"golang.org/x/net/context"

	"github.com/vmware/govmomi"
	"github.com/vmware/govmomi/find"
	"github.com/vmware/govmomi/object"
	"github.com/vmware/govmomi/session"
	"github.com/vmware/govmomi/vim25"
	"github.com/vmware/govmomi/vim25/soap"
	"github.com/vmware/govmomi/vim25/types"
	"github.com/vmware/vic/lib/config"
	"github.com/vmware/vic/pkg/errors"
	"github.com/vmware/vic/pkg/vsphere/extraconfig"
)

// Config contains the configuration used to create a Session.
type Config struct {
	// SDK URL or proxy
	// TODO make sure this doesn't contain credentials
	Service string
	// Allow insecure connection to Service
	Insecure bool
	// Target thumbprint
	Thumbprint string
	// Keep alive duration
	Keepalive time.Duration

	ClusterPath    string
	DatacenterPath string
	DatastorePath  string
	HostPath       string
	PoolPath       string

	// keypair for the vSphere extension
	ExtensionCert string
	ExtensionKey  string

	// confusingly vSphere calls this the extension key
	ExtensionName string
}

// HasCertificate checks for presence of a certificate and keyfile
func (c *Config) HasCertificate() bool {
	return c.ExtensionCert != "" && c.ExtensionKey != ""
}

// Session caches vSphere objects obtained by querying the SDK.
type Session struct {
	*govmomi.Client

	*Config

	Cluster    *object.ComputeResource
	Datacenter *object.Datacenter
	Datastore  *object.Datastore
	Host       *object.HostSystem
	Pool       *object.ResourcePool

	Finder *find.Finder

	folders *object.DatacenterFolders
}

// NewSession creates a new Session struct. If config is nil,
// it creates a Flags object from the command line arguments or
// environment, and uses that instead to create a Session.
func NewSession(config *Config) *Session {
	return &Session{Config: config}
}

// Vim25 returns the vim25.Client to the caller
func (s *Session) Vim25() *vim25.Client {
	return s.Client.Client
}

// IsVC returns whether the session is backed by VC
func (s *Session) IsVC() bool {
	return s.Client.IsVC()
}

// IsVSAN returns whether the datastore used in the session is backed by VSAN
func (s *Session) IsVSAN(ctx context.Context) bool {
	dsType, _ := s.Datastore.Type(ctx)

	return dsType == types.HostFileSystemVolumeFileSystemTypeVsan
}

// Create accepts a Config and returns a Session with the cached vSphere resources.
func (s *Session) Create(ctx context.Context) (*Session, error) {
	var vchExtraConfig config.VirtualContainerHostConfigSpec
	source, err := extraconfig.GuestInfoSource()
	if err != nil {
		return nil, err
	}

	extraconfig.Decode(source, &vchExtraConfig)

	s.ExtensionKey = vchExtraConfig.ExtensionKey
	s.ExtensionCert = vchExtraConfig.ExtensionCert
	s.ExtensionName = vchExtraConfig.ExtensionName
	s.Thumbprint = vchExtraConfig.TargetThumbprint

	_, err = s.Connect(ctx)
	if err != nil {
		return nil, err
	}

	// we're treating this as an atomic behaviour, so log out if we failed
	defer func() {
		if err != nil {
			s.Client.Logout(ctx)
		}
	}()

	_, err = s.Populate(ctx)
	if err != nil {
		return nil, err
	}

	return s, nil
}

// Connect establishes the connection for the session but nothing more
func (s *Session) Connect(ctx context.Context) (*Session, error) {
	soapURL, err := soap.ParseURL(s.Service)
	if soapURL == nil || err != nil {
		return nil, errors.Errorf("SDK URL (%s) could not be parsed: %s", s.Service, err)
	}

	// LoginExtensionByCertificate proxies connections to a virtual host (sdkTunnel:8089) and
	// Go's http.Transport.DialTLS isn't called when using a proxy.  Even if using a known CA,
	// "sdkTunnel" does not pass Go's tls.VerifyHostname check.
	// We are moving away from LoginExtensionByCertificate anyhow, so disable thumbprint checks for now.
	if s.HasCertificate() {
		s.Insecure = true
	}

	// Update the service URL with expanded defaults
	s.Service = soapURL.String()

	soapClient := soap.NewClient(soapURL, s.Insecure)
	var login func(context.Context) error

	if s.HasCertificate() {
		cert, err2 := tls.X509KeyPair([]byte(s.ExtensionCert), []byte(s.ExtensionKey))
		if err2 != nil {
			return nil, errors.Errorf("Unable to load X509 key pair(%s,%s): %s",
				s.ExtensionCert, s.ExtensionKey, err2)
		}

		soapClient.SetCertificate(cert)
		log.Debugf("Using login by extension %s certificate", s.ExtensionName)

		login = func(ctx context.Context) error {
			return s.LoginExtensionByCertificate(ctx, s.ExtensionName, "")
		}
	} else {
		log.Debugf("Using to login by username/password")

		login = func(ctx context.Context) error {
			return s.Client.Login(ctx, soapURL.User)
		}
	}

	soapClient.SetThumbprint(soapURL.Host, s.Thumbprint)

	// TODO: option to set http.Client.Transport.TLSClientConfig.RootCAs

	vimClient, err := vim25.NewClient(ctx, soapClient)
	if err != nil {
		return nil, errors.Errorf("Failed to connect to %s: %s", soapURL.Host, err)
	}

	if s.Keepalive != 0 {
		// TODO: add login() to the keep alive handler
		vimClient.RoundTripper = session.KeepAlive(soapClient, s.Keepalive)
	}

	// TODO: get rid of govmomi.Client usage, only provides a few helpers we don't need.
	s.Client = &govmomi.Client{
		Client:         vimClient,
		SessionManager: session.NewManager(vimClient),
	}

	err = login(ctx)
	if err != nil {
		return nil, errors.Errorf("Failed to log in to %s: %s", soapURL.Host, err)
	}

	s.Finder = find.NewFinder(s.Vim25(), false)
	// log high-level environment information
	s.logEnvironmentInfo()
	return s, nil
}

// Populate resolves the set of cached resources that should be presented
// This returns accumulated error detail if there is ambiguity, but sets all
// unambiguous or correct resources.
func (s *Session) Populate(ctx context.Context) (*Session, error) {
	// Populate s
	var errs []string
	var err error

	finder := s.Finder

	log.Debug("vSphere resource cache populating...")
	s.Datacenter, err = finder.DatacenterOrDefault(ctx, s.DatacenterPath)
	if err != nil {
		errs = append(errs, fmt.Sprintf("Failure finding dc (%s): %s", s.DatacenterPath, err.Error()))
	} else {
		finder.SetDatacenter(s.Datacenter)
		log.Debugf("Cached dc: %s", s.DatacenterPath)
	}

	finder.SetDatacenter(s.Datacenter)

	s.Cluster, err = finder.ComputeResourceOrDefault(ctx, s.ClusterPath)
	if err != nil {
		errs = append(errs, fmt.Sprintf("Failure finding cluster (%s): %s", s.ClusterPath, err.Error()))
	} else {
		log.Debugf("Cached cluster: %s", s.ClusterPath)
	}

	s.Datastore, err = finder.DatastoreOrDefault(ctx, s.DatastorePath)
	if err != nil {
		errs = append(errs, fmt.Sprintf("Failure finding ds (%s): %s", s.DatastorePath, err.Error()))
	} else {
		log.Debugf("Cached ds: %s", s.DatastorePath)
	}

	s.Host, err = finder.HostSystemOrDefault(ctx, s.HostPath)
	if err != nil {
		if _, ok := err.(*find.DefaultMultipleFoundError); !ok || !s.IsVC() {
			errs = append(errs, fmt.Sprintf("Failure finding host (%s): %s", s.HostPath, err.Error()))
		}
	} else {
		log.Debugf("Cached host: %s", s.HostPath)
	}

	s.Pool, err = finder.ResourcePoolOrDefault(ctx, s.PoolPath)
	if err != nil {
		errs = append(errs, fmt.Sprintf("Failure finding pool (%s): %s", s.PoolPath, err.Error()))
	} else {
		log.Debugf("Cached pool: %s", s.PoolPath)
	}

	if len(errs) > 0 {
		log.Debugf("Error count populating vSphere cache: (%d)", len(errs))
		return nil, errors.New(strings.Join(errs, "\n"))
	}
	log.Debug("vSphere resource cache populated...")
	return s, nil
}

func (s *Session) logEnvironmentInfo() {
	a := s.ServiceContent.About
	log.WithFields(log.Fields{
		"Name":        a.Name,
		"Vendor":      a.Vendor,
		"Version":     a.Version,
		"Build":       a.Build,
		"OS Type":     a.OsType,
		"API Type":    a.ApiType,
		"API Version": a.ApiVersion,
		"Product ID":  a.ProductLineId,
		"UUID":        a.InstanceUuid,
	}).Debug("Session Environment Info: ")
	return
}

func (s *Session) Folders(ctx context.Context) *object.DatacenterFolders {
	var err error

	if s.folders != nil {
		return s.folders
	}

	if s.folders, err = s.Datacenter.Folders(ctx); err != nil {
		return nil
	}

	return s.folders
}
