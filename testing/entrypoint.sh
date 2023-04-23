#!/bin/bash

# check if file exists, no need to do configuration again
if [ ! -f "/usr/sbin/sshd" ]; then
    # change password
    echo 'root:password' | chpasswd

    # install open ssh
    apk update && apk add openssh
    echo "PubkeyAuthentication yes" >> /etc/ssh/sshd_config
    echo "PermitRootLogin without-password" >> /etc/ssh/sshd_config

    # generate host key
    ssh-keygen -A

    # authorize client key
    mkdir -p /root/.ssh
    rm -f /root/.ssh/authorized_keys
    cat /root/easshy.key.pub >> /root/.ssh/authorized_keys
fi

# disable searching for go.mod
export GO111MODULE=auto

# run ssh server
/usr/sbin/sshd -D
