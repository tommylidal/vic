Test 1-25 - Docker Port Mapping
=======

# Purpose:
To verify that docker create works with the -p option

# References:
[1 - Docker Command Line Reference](https://docs.docker.com/engine/reference/commandline/create/)

# Environment:
This test requires that a vSphere server is running and available

# Test Cases

## Create container with port mappings
1. Deploy VIC appliance to vSphere server
2. Issue docker create -it -p 10000:80 -p 10001:80 --name webserver nginx
3. Issue docker start webserver
4. Issue curl vch-ip:10000 --connect-timeout 20
5. Issue curl vch-ip:10001 --connect-timeout 20
6. Issue docker stop webserver
7. Issue curl vch-ip:10000
8. Issue curl vch-ip:10001

### Expected Outcome:
* Steps 2-6 should all return without error
* Steps 7-8 should both return error


## Create container with conflicting port mapping
1. Issue docker create -it -p 8083:80 --name webserver2 nginx
2. Issue docker create -it -p 8083:80 --name webserver3 nginx
3. Issue docker start webserver2
4. Issue docker start webserver3

### Expected Outcome:
* Steps 1-3 should all return without error
* Step 4 should return error


## Create container with port range
1. Issue docker create -it -p 8081-8088:80 --name webserver5 nginx

### Expected Outcome:
* Step 1 should return error


## Create container with host IP
1. Issue docker create -it -p 10.10.10.10:8088:80 --name webserver5 nginx

### Expected Outcome:
* Step 1 should return error


## Create container without specifying host port
1. Issue docker create -it -p 6379 --name test-redis redis:alpine
2. Issue docker start test-redis
3. Issue docker stop test-redis

### Expected Outcome:
* Steps 1-3 should return without error


## Run after exit remapping mapped ports
1. Deploy VIC appliance to vSphere server
2. Issue `docker run -i -p 1900:9999 -p 2200:2222 busybox /bin/top`
3. Issue `q` to the container
4. Issue `docker run -i -p 1900:9999 -p 3300:3333 busybox /bin/top`
5. Issue `q` to the container

### Expected Outcome:
* All steps should return without without error


## Remap mapped ports after OOB Stop
1. Deploy VIC appliance to vSphere server
2. Issue `docker create -it -p 10000:80 -p 10001:80 busybox`
3. Issue docker start <containerID> to the VIC appliance
4. Power off the container with govc
5. Issue `docker create -it -p 10000:80 -p 20000:2222 busybox`
6. Issue docker start <containerID> to the VIC appliance

### Expected Outcome:
* All steps should return without without error
