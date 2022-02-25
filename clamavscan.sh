#!/bin/bash
# This script runs a clamav scan against images which
# are built using RHEL8/CENTOS8/UBI8/UBI-M8 base images

DEFAULT_CONTAINER_TOOL=podman
CONTAINER_TOOL=$DEFAULT_CONTAINER_TOOL

DEFAULT_PACKAGE_MANAGER=microdnf
PACKAGE_MANAGER=$DEFAULT_PACKAGE_MANAGER

function set_container_tool {
  if [ "$1" == "" ]; then
    echo "Container tool can't be set as blank"
    exit 1
  fi
  CONTAINER_TOOL="$1"
}

function set_image_name {
  if [ "$1" == "" ]; then
    echo "Image name can't be set as blank"
    exit 1
  fi
  IMAGE_NAME=$1
}

function set_package_manager {
  if [ "$1" == "" ]; then
    echo "Package manager can't be set as blank"
    exit 1
  fi
  PACKAGE_MANAGER=$1
}

function print_usage {
   echo
   basename "$0"
   echo "    -c      - Choice of container tool (podman/docker). Default is podman"
   echo "    -i      - Image name for which scan has to be run"
   echo "    -p      - Choice of package manager for installing clamav (microdnf/dnf/yum). Default is microdnf"
   echo
   echo "Set CA_CERTS env to point to a directory in case you want to copy Dell CA_CERTS to the container"
}

function stop_container {
  $CONTAINER_TOOL stop "$CONTAINER_NAME"
}

# Read options
while getopts 'hi:c:p:' flag; do
  case "${flag}" in
    c) set_container_tool "$OPTARG" ;;
    i) set_image_name "$OPTARG" ;;
    p) set_package_manager "$OPTARG" ;;
    h) print_usage
       exit 0;;
    *) print_usage
       exit 1 ;;
  esac
done

if [ "$IMAGE_NAME" == "" ]; then
  echo "You must specify the name of the image to be scanned"
fi

now=$(date +%F%H%M%S)
CONTAINER_NAME="clamavscan$now"

# Run the container in detached mode
echo "########################################################"
echo "Starting a container in detached mode for the scan with the name: $CONTAINER_NAME"
echo "$CONTAINER_TOOL" run -itd --rm --entrypoint /bin/bash --name "$CONTAINER_NAME" "$IMAGE_NAME";
echo "########################################################"
if ! $CONTAINER_TOOL run -itd --rm --entrypoint /bin/bash --name "$CONTAINER_NAME" "$IMAGE_NAME";
then
  echo "Failed to start the container for scan"
  exit 1
fi

# Install the EPEL8 rpm
echo "########################################################"
echo "Setting up EPEL8 repo"
echo "$CONTAINER_TOOL" exec -it "$CONTAINER_NAME" rpm -ivh https://dl.fedoraproject.org/pub/epel/epel-release-latest-8.noarch.rpm
echo "########################################################"
if ! $CONTAINER_TOOL exec -it "$CONTAINER_NAME" rpm -ivh https://dl.fedoraproject.org/pub/epel/epel-release-latest-8.noarch.rpm;
then
  echo "Failed to setup the EPEL8 repo"
  stop_container
  exit 1
fi

# Install ClamAV, freshclam
echo "########################################################"
echo "Installing clamav, clamav-update clamd"
echo "$CONTAINER_TOOL" exec -it "$CONTAINER_NAME" "$PACKAGE_MANAGER" install -y clamav clamav-update clamd
echo "########################################################"
if ! "$CONTAINER_TOOL" exec -it "$CONTAINER_NAME" "$PACKAGE_MANAGER" install -y clamav clamav-update clamd;
then
  echo "Failed to install clamav packages"
  stop_container
  exit 1
fi

# Install CA certs if CA_CERTS env is set
if [ -z ${CA_CERTS+x} ]; then
  echo "CA_CERTS env is not set. The ClamAV database update may fail"
else
  echo "########################################################"
  echo "CA_CERTS is set to $CA_CERTS. Copying CA certificates to the container: $CONTAINER_NAME"
  for f in "$CA_CERTS"/*
  do
    echo "$CONTAINER_TOOL" cp "$f" "$CONTAINER_NAME":/etc/pki/ca-trust/source/anchors/
    if ! $CONTAINER_TOOL cp "$f" "$CONTAINER_NAME":/etc/pki/ca-trust/source/anchors/;
    then
      echo "Failed to copy $f into $CONTAINER_NAME"
      stop_container
      exit 1
    fi
  done
  echo "########################################################"
  echo "Updating CA trust inside the container: $CONTAINER_NAME"
  # Update the CA certs inside the container
  echo "$CONTAINER_TOOL" exec -it "$CONTAINER_NAME" update-ca-trust --force-enable
  if ! $CONTAINER_TOOL exec -it "$CONTAINER_NAME" update-ca-trust --force-enable;
  then
    echo "Failed to update the CA certificates"
    stop_container
    exit 1
  fi
fi

# Update the clamav database
echo "########################################################"
echo "Updating clamav database"
echo "$CONTAINER_TOOL" exec -it "$CONTAINER_NAME" freshclam
echo "########################################################"
if ! "$CONTAINER_TOOL" exec -it "$CONTAINER_NAME" freshclam;
then
  echo "Failed to update the clamav database"
  stop_container
  exit 1
fi

echo "########################################################"
echo "Running the clamav scan "
echo "$CONTAINER_TOOL" exec -it "$CONTAINER_NAME" clamscan -r -i --exclude-dir=/sys /
echo "########################################################"
RC=0
if ! $CONTAINER_TOOL exec -it "$CONTAINER_NAME" clamscan -r -i --exclude-dir=/sys /;
then
    echo "ClamAV exit code: $?"
    echo "########################################################"
    echo "ClamAV scan reported some errors"
    echo "Please examine the results carefully"
    RC=1
fi
echo "########################################################"
echo "Finished the scan"
# Stop the container
stop_container
exit $RC
