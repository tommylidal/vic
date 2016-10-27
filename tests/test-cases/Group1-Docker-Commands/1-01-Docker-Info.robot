*** Settings ***
Documentation  Test 1-01 - Docker Info
Resource  ../../resources/Util.robot
Suite Setup  Install VIC Appliance To Test Server
Suite Teardown  Cleanup VIC Appliance On Test Server

*** Test Cases ***
Basic Info
    Log To Console  \nRunning docker info command...
    ${output}=  Run  docker %{VCH-PARAMS} info
    Log  ${output}
    Should Contain  ${output}  vSphere

Debug Info
    ${status}=  Get State Of Github Issue  780
    Run Keyword If  '${status}' == 'closed'  Fail  Test 1-1-Docker-Info.robot needs to be updated now that Issue #780 has been resolved
    #Log To Console  \nRunning docker -D info command...
    #${output}=  Run  docker %{VCH-PARAMS} -D info
    #Log  ${output}
    #Should Contain  ${output}  Debug mode

Correct container count
    ${rc}  ${output}=  Run And Return Rc And Output  docker %{VCH-PARAMS} info
    Should Be Equal As Integers  ${rc}  0
    Should Not Contain  ${output}  Error
    Should Contain  ${output}  Containers: 0
    ${rc}  ${output}=  Run And Return Rc And Output  docker %{VCH-PARAMS} pull busybox
    Should Be Equal As Integers  ${rc}  0
    Should Not Contain  ${output}  Error
    ${rc}  ${cid}=  Run And Return Rc And Output  docker %{VCH-PARAMS} create busybox
    Should Be Equal As Integers  ${rc}  0
    Should Not Contain  ${cid}  Error
    ${rc}  ${output}=  Run And Return Rc And Output  docker %{VCH-PARAMS} info
    Should Be Equal As Integers  ${rc}  0
    Should Not Contain  ${output}  Error
    Should Contain  ${output}  Containers: 1
    ${rc}  ${output}=  Run And Return Rc And Output  docker %{VCH-PARAMS} start ${cid}
    Should Be Equal As Integers  ${rc}  0
    Should Not Contain  ${output}  Error
    ${rc}  ${output}=  Run And Return Rc And Output  docker %{VCH-PARAMS} info
    Should Be Equal As Integers  ${rc}  0
    Should Not Contain  ${output}  Error
    Should Contain  ${output}  Containers: 1
    Should Contain  ${output}  Running: 1