# vSphere Integrated Containers Engine Networking Overview #

Virtual container hosts connect to different types of network. 

 ![vSphere Container Host Management Network](graphics/vch-net.png)

This topic provides an overview of the different network types. 

## Container Bridge Networks ##
The network or networks that container VMs use to communicate with each other. Each virtual container host requires a unique bridge network.

You define the bridge networks by setting the `bridge-network` option when you run `vic-machine create`.  For  more detailed information about bridge networks, see the section on the `bridge-network` option in [Virtual Container Host Deployment Options](vch_installer_options.md#bridge).

Container application developers can also use `docker network create` to create additional bridge networks. You can define a range of IP addresses that additional bridge networks can use by defining the `bridge-network-range` option when you run `vic-machine create`. For  more detailed information about  how to set bridge network ranges, see the section on the [`bridge-network-range` option](vch_installer_options.md#bridge-range). 

 ![Container Bridge Network](graphics/vch-bridge-net.png)

## External Network  ##
The network that container VMs use to connect to the internet. Containers can use this external network to publish network services. After defining the external network, you can deploy containers directly on the external interface.

You define the external network by setting the `external-network` option when you run `vic-machine create`. For  more detailed information about management networks, see the section on the `external-network` option in [Virtual Container Host Deployment Options](vch_installer_options.md#external-network).

 ![External Network](graphics/vch-external-net.png)

## vSphere Management Network ##

The network for communication between the virtual container host and vCenter Server and ESXi hosts. This network also serves as a tether within container VMs for communication with the virtual container host.

You define the management network by setting the `management-network` option when you run `vic-machine create`. For  more detailed information about management networks, see the section on the `management-network` option in [Virtual Container Host Deployment Options](vch_installer_options.md#management-network).

 ![vSphere Management Network](graphics/vch-management-net.png)

## Docker Management Endpoint Network ##

Connects virtual container hosts to Docker clients and isolates the Docker endpoints from the more public external network.

You define the Docker management endpoint network by setting the `client-network` option when you run `vic-machine create`. For  more detailed information about Docker management endpoint networks, see the section on the `client-network` option in [Virtual Container Host Deployment Options](vch_installer_options.md#client-network).

 ![Docker Management Endpoint Network](graphics/vch-docker-net.png)

## Container Networks ##

Networks for container VMs to use for external communication when container developers  run `docker run` or `docker create` with the `--net` option. For  more detailed information about setting up container networks, see the sections on the `container-network-xxx` options in [Virtual Container Host Deployment Options](vch_installer_options.md#container-network).


## The Port Layer

You can configure networks that are tied into the vSphere infrastructure. Pre-configured networks available to a virtual container host are determined by the networks that you define when you configure the virtual container host.

The port layer augments the vSphere API with low level, platform-specific primitives to allow you to implement a simple container engine:

- Port Layer Execution: Handles container management, such as create, start, and stop. 
- Port Layer Interaction: Handles interaction with a running container. 
- Port Layer Networking: Handles specific vSphere network mappings into the Docker network namespace as well as mapping existing network entities such as database servers into the Docker container namespace with defined aliases.
- Port Layer Storage: Provides storage manipulation, including container image storage, layering with volume creation and manipulation. imagec, the docker registry client library, uses this component to translate registry images into a layered format that VMDK disk chains can use directly.


## Tether Process ##

The tether process is a minimal agent in the container VM that starts and stops processes and provides monitoring statistics. 