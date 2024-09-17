#!/bin/bash
# Copyright © 2020 Dell Inc. or its subsidiaries. All Rights Reserved.
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#      http://www.apache.org/licenses/LICENSE-2.0
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.
#
# This script will build an image for the PowerMax CSI Driver
# Before running this script, make sure that you have Docker engine (> v17)
# or podman installed on your system
# If you are going to push the image to an image repo, make sure that you are logged in

# This script parses the config file(s) present in the same folder to use as an environment for the build


# Defaults
IMAGE_NAME="csi-powermax"
REVPROXY_IMAGE_NAME="csipowermax-reverseproxy"
NOPROMPT=false
PUSH_IMAGE=false
BUILD_REVPROXY=false
OVERWRITE_IMAGE=false
EXTRA_TAGS=false
NOCACHE=

function print_usage {
   echo
   echo "`basename ${0}`"
   echo "    -p      - Push the image to the repo specified via config"
   echo "    -y      - Don't prompt the user"
   echo "    -o      - Overwrite existing local/remote image"
   echo "    -e      - Create additionals tags for latest, minor and major versions"
   echo "    -i      - Set the image type. Accepted values are ubim, ubimicro, ubi, centos and rhel"
   echo "    -r      - Build the CSI PowerMax ReverseProxy image along with driver image"
   echo "    -c      - Delete the local image after a successful build"
   echo "    -n      - Build csi-powermax image with no cache option"
   echo
   echo "Default values are specified via the .build.config file. They can be overridden by creating a .build.config.user file"
}

function git_version {
   local gitdesc=$(git describe --long)
   local version="${gitdesc%%-*}"
   MAJOR_VERSION=$(echo $version | cut -d. -f1)
   MINOR_VERSION=$(echo $version | cut -d. -f2)
   PATCH_NUMBER=$(echo $version | cut -d. -f3)
   BUILD_NUMBER_FROM_GIT=$(sed -e 's#.*-\(\)#\1#' <<< "${gitdesc%-*}")
   printf -v TEMP_BUILD_NUMBER "%03d" $BUILD_NUMBER_FROM_GIT
}

function parse_config {
   local prefix=$2
   local s='[[:space:]]*' w='[a-zA-Z0-9_]*' fs=$(echo @|tr @ '\034')
   sed -ne "s|^\($s\):|\1|" \
        -e "s|^\($s\)\($w\)$s:$s[\"']\(.*\)[\"']$s\$|\1$fs\2$fs\3|p" \
        -e "s|^\($s\)\($w\)$s:$s\(.*\)$s\$|\1$fs\2$fs\3|p"  $1 |
   awk -F$fs '{
      indent = length($1)/2;
      vname[indent] = $2;
      for (i in vname) {if (i > indent) {delete vname[i]}}
      if (length($3) > 0) {
         vn=""; for (i=0; i<indent; i++) {vn=(vn)(vname[i])("_")}
         printf("%s%s%s=\"%s\"\n", "'$prefix'",vn, $2, $3);
      }
   }'
}

function build_source_image_repo_name {
   # Check if source image repo is set
   if [ "$SOURCE_IMAGE_TYPE" = "centos" ]; then
      SOURCE_IMAGE_REPO=$CENTOS_REPO
      SOURCE_IMAGE_REPO_NAMESPACE=$CENTOS_NAMESPACE
   elif [ "$SOURCE_IMAGE_TYPE" = "rhel" ]; then
      SOURCE_IMAGE_REPO=$RHEL_REPO
      SOURCE_IMAGE_REPO_NAMESPACE=$RHEL_NAMESPACE
   elif [ "$SOURCE_IMAGE_TYPE" = "ubi" ]; then
      SOURCE_IMAGE_REPO=$UBI_REPO
      SOURCE_IMAGE_REPO_NAMESPACE=$UBI_NAMESPACE
   elif [ "$SOURCE_IMAGE_TYPE" = "ubim" ]; then
      SOURCE_IMAGE_REPO=$UBIM_REPO
      SOURCE_IMAGE_REPO_NAMESPACE=$UBIM_NAMESPACE
   elif [ "$SOURCE_IMAGE_TYPE" = "ubimicro" ]; then
      SOURCE_IMAGE_REPO=$UBIMICRO_REPO
      SOURCE_IMAGE_REPO_NAMESPACE=$UBIMICRO_NAMESPACE
   fi
   echo "SOURCE_IMAGE_REPO is set to: $SOURCE_IMAGE_REPO"
   echo "SOURCE_IMAGE_REPO_NAMESPACE is set to: $SOURCE_IMAGE_REPO_NAMESPACE"
   TRIMMED_SOURCE_IMAGE_REPO=$(echo $SOURCE_IMAGE_REPO | sed 's:/*$::')
   TRIMMED_SOURCE_IMAGE_REPO_NAMESPACE=$(echo $SOURCE_IMAGE_REPO_NAMESPACE | sed 's:/*$::')
   if [[ "$TRIMMED_SOURCE_IMAGE_REPO" == "" ]]; then
         # This means that we are pulling from an repository for which an unqualified search can be done
         SOURCE_REPO=$TRIMMED_SOURCE_IMAGE_REPO_NAMESPACE
   else
     SOURCE_REPO="$TRIMMED_SOURCE_IMAGE_REPO/$TRIMMED_SOURCE_IMAGE_REPO_NAMESPACE"
   fi
   echo "Source Image will be pulled from: $SOURCE_REPO"
}

function build_image_tags {
   # Check if image repo is set
   if [ -n "$BUILD_REPO" ]; then
      echo "BUILD_REPO is set to:" $BUILD_REPO
      TRIMMED_BUILD_REPO=$(echo $BUILD_REPO | sed 's:/*$::')
      if [ -n "$BUILD_NAMESPACE" ]; then
         TRIMMED_BUILD_NAMESPACE=$(echo $BUILD_NAMESPACE | sed 's:/*$::')
      fi
      if [ -n "$TRIMMED_BUILD_NAMESPACE" ]; then
         IMAGE_TAG="$TRIMMED_BUILD_REPO/$TRIMMED_BUILD_NAMESPACE/$IMAGE_NAME"
         IMAGE_VERSION_TAG="$TRIMMED_BUILD_REPO/$TRIMMED_BUILD_NAMESPACE/$IMAGE_NAME:$VERSION"
         REVPROXY_IMAGE_VERSION_TAG="$TRIMMED_BUILD_REPO/$TRIMMED_BUILD_NAMESPACE/$REVPROXY_IMAGE_NAME:$VERSION"
      else
         IMAGE_TAG="$TRIMMED_BUILD_REPO/$IMAGE_NAME"
         IMAGE_VERSION_TAG="$TRIMMED_BUILD_REPO/$IMAGE_NAME:$VERSION"
         REVPROXY_IMAGE_VERSION_TAG="$TRIMMED_BUILD_REPO/$REVPROXY_IMAGE_NAME:$VERSION"
      fi
   else
      echo "BUILD_REPO not specified. Building an image with name:" $IMAGE_NAME
      IMAGE_VERSION_TAG="$IMAGE_NAME:$VERSION"
   fi
   echo "IMAGE_VERSION_TAG is set to:" $IMAGE_VERSION_TAG
   if [ "$BUILD_REVPROXY" = true ]; then
     echo "REVPROXY_IMAGE_VERSION_TAG is set to:" $REVPROXY_IMAGE_VERSION_TAG
   fi
   #Creating the directory if it doesnt exists
   dir_email_path="/var/jenkins/workspace"
   if [ ! -d $dir_email_path ] ; then
      mkdir -p $dir_email_path
      echo "Directory created"
   else 
      echo " Directory already exists"
   fi
   # Command to append the version to a local file, local file will be referred for email formatting(To build artifactory URL)
   echo $IMAGE_VERSION_TAG > /var/jenkins/workspace/patch1_info
   if [ "$EXTRA_TAGS" = true ]; then
      if [ -n "$BUILD_REPO" ]; then
         IMAGE_LATEST_TAG="$IMAGE_TAG:latest"
         IMAGE_VERSION_WITH_PATCH_TAG="$IMAGE_TAG:$MAJOR_VERSION.$MINOR_VERSION.$PATCH_NUMBER"
      else
         echo "BUILD_REPO not specified but EXTRA tags are enabled. Skipping creation of extra tags"
         EXTRA_TAGS=false
      fi
   fi
}

function build_image {
   # Check if a local image exists with the same tag
   echo "###################### BUILDING IMAGE ########################"
   echo $BUILDCMD inspect --type=image $IMAGE_VERSION_TAG
   $BUILDCMD image inspect $IMAGE_VERSION_TAG >/dev/null 2>&1 && image_exists=true || image_exists=false
   if [[ $image_exists == "true" ]] && [[ $OVERWRITE_IMAGE == "false" ]] ; then
      echo "A local image with the same tag exists. Enable the over-write option (-o) and run the script again"
      exit 1
   fi
   if [ "$EXTRA_TAGS" = true ]; then
      echo $BUILDCMD build $NOCACHE -t $IMAGE_VERSION_TAG\
                -t $IMAGE_LATEST_TAG\
                -t $IMAGE_VERSION_WITH_PATCH_TAG\
                -f csi-powermax/Dockerfile.build .\
                --build-arg BUILD_NUMBER=$BUILD_NUMBER\
                --build-arg BUILD_TYPE=$BUILD_TYPE\
                --build-arg GOIMAGE="$DEFAULT_GOIMAGE"\
                --build-arg SOURCE_IMAGE_TAG=$SOURCE_IMAGE_TAG\
                --build-arg SOURCE_REPO=$SOURCE_REPO\
                --build-arg IMAGE_TYPE=$IMAGE_TYPE\
                $DOCKEROPT
      (cd .. &&
       $BUILDCMD build $NOCACHE -t "$IMAGE_VERSION_TAG"\
                -t "$IMAGE_LATEST_TAG"\
                -t "$IMAGE_VERSION_WITH_PATCH_TAG"\
                -f csi-powermax/Dockerfile.build .\
                --build-arg BUILD_NUMBER="$BUILD_NUMBER"\
                --build-arg BUILD_TYPE="$BUILD_TYPE"\
                --build-arg GOIMAGE="$DEFAULT_GOIMAGE"\
                --build-arg SOURCE_IMAGE_TAG="$SOURCE_IMAGE_TAG"\
                --build-arg SOURCE_REPO="$SOURCE_REPO"\
                --build-arg IMAGE_TYPE="$IMAGE_TYPE"\
                $DOCKEROPT)
       if [ "$BUILD_REVPROXY" = true ]; then
         echo $BUILDCMD build -t "$REVPROXY_IMAGE_VERSION_TAG"
         (cd csireverseproxy && $BUILDCMD build -t "$REVPROXY_IMAGE_VERSION_TAG" --build-arg GOIMAGE="$DEFAULT_GOIMAGE" . )
       fi
   else
      echo $BUILDCMD build $NOCACHE -t $IMAGE_VERSION_TAG\
                -f csi-powermax/Dockerfile.build .\
                --build-arg BUILD_NUMBER=$BUILD_NUMBER\
                --build-arg BUILD_TYPE=$BUILD_TYPE\
                --build-arg GOIMAGE=$DEFAULT_GOIMAGE\
                --build-arg SOURCE_IMAGE_TAG=$SOURCE_IMAGE_TAG\
                --build-arg SOURCE_REPO=$SOURCE_REPO\
                --build-arg IMAGE_TYPE="$IMAGE_TYPE"\
                $DOCKEROPT
      (cd .. &&
       $BUILDCMD build $NOCACHE -t "$IMAGE_VERSION_TAG"\
                -f csi-powermax/Dockerfile.build .\
                --build-arg BUILD_NUMBER="$BUILD_NUMBER"\
                --build-arg BUILD_TYPE="$BUILD_TYPE"\
                --build-arg GOIMAGE="$DEFAULT_GOIMAGE"\
                --build-arg SOURCE_IMAGE_TAG="$SOURCE_IMAGE_TAG"\
                --build-arg SOURCE_REPO="$SOURCE_REPO"\
                --build-arg IMAGE_TYPE="$IMAGE_TYPE"\
                $DOCKEROPT)
       if [ "$BUILD_REVPROXY" = true ]; then
         echo $BUILDCMD build -t "$REVPROXY_IMAGE_VERSION_TAG" .
         (cd csireverseproxy && $BUILDCMD build -t "$REVPROXY_IMAGE_VERSION_TAG" . )
       fi
   fi
}

function push_image {
   echo $BUILDCMD push $IMAGE_TAG
   $BUILDCMD push $IMAGE_VERSION_TAG
   if [ "$BUILD_REVPROXY" = true ]; then
     $BUILDCMD push $REVPROXY_IMAGE_VERSION_TAG
   fi
   if [ "$EXTRA_TAGS" = true ]; then
      echo $BUILDCMD push $IMAGE_LATEST_TAG
      $BUILDCMD push $IMAGE_LATEST_TAG
      echo $BUILDCMD push $IMAGE_VERSION_WITH_PATCH_TAG
      $BUILDCMD push $IMAGE_VERSION_WITH_PATCH_TAG
   fi
}

function set_image_type {
   input_image_type=$1
   valid_image_type='false'
   if [[ ( $input_image_type == "CENTOS" ) || ( $input_image_type == "centos" ) ]]; then
      SOURCE_IMAGE_TYPE="centos"
      valid_image_type='true'
   elif [[ ( $input_image_type == "RHEL" ) || ( $input_image_type == "rhel" ) ]]; then
      SOURCE_IMAGE_TYPE="rhel"
      valid_image_type='true'
   elif [[ ( $input_image_type == "UBI" ) || ( $input_image_type == "ubi" ) ]]; then
      SOURCE_IMAGE_TYPE="ubi"
      valid_image_type='true'
   elif [[ ( $input_image_type == "UBIM" ) || ( $input_image_type == "ubim" ) ]]; then
      SOURCE_IMAGE_TYPE="ubim"
      valid_image_type='true'
   elif [[ ( $input_image_type == "UBIMICRO" ) || ( $input_image_type == "ubimicro" ) ]]; then
      SOURCE_IMAGE_TYPE="ubimicro"
      valid_image_type='true'
   fi   
   if [ "$valid_image_type" = false ] ; then
      echo "Invalid image type specified"
      exit 1
   fi
}
IMAGE_TYPE_SET=false
# Read options
while getopts 'cpnyheori:' flag; do
  case "${flag}" in
    c) DELETE_IMAGE='true' ;;
    p) PUSH_IMAGE='true' ;;
    n) NOCACHE='--no-cache' ;;
    y) NOPROMPT='true' ;;
    e) EXTRA_TAGS='true' ;;
    o) OVERWRITE_IMAGE='true' ;;
    r) BUILD_REVPROXY='true' ;;
    i) IMAGE_TYPE_SET=true;set_image_type $OPTARG;;
    h) print_usage
       exit 0;;
    *) print_usage
       exit 1 ;;
  esac
done

if [ "$IMAGE_TYPE_SET" = false ]; then
    echo "Missing argument: -i must be supplied to provide the image type" >&2
    exit 1
fi

BUILDCMD="docker"
DOCKEROPT="--format=docker"

if [[ ( $SOURCE_IMAGE_TYPE == "ubi" ) || ( $SOURCE_IMAGE_TYPE == "ubim" ) || ( $SOURCE_IMAGE_TYPE == "ubimicro" ) || ( $SOURCE_IMAGE_TYPE == "rhel" ) ]]; then
   command -v podman
   if [ $? -eq 0 ]; then
      echo "Using podman for building image"
      BUILDCMD="podman"
   else
      echo "podman must be installed for building RHEL/UBI based image"
      exit 1
   fi
else
   command -v docker
   if [ $? -eq 0 ]; then
      echo "Using docker for building image"
      BUILDCMD="docker"
      DOCKEROPT=""
   else
      echo "Couldn't find docker. Looking for podman"
      command -v podman
      if [ $? -eq 0 ]; then
         echo "Using podman for building image"
         BUILDCMD="podman"
      else
         echo "Failed to find docker or podman. Exiting with failure"
         exit 1
      fi
   fi
fi

# After finding the toolset, fail immediately after any error
set -e

# Get the version from git tag
git_version

# Read the config and the user config (if specified)
eval "$(parse_config .build.config)"
if [ -e .build.config.user ]; then
   eval "$(parse_config .build.config.user)"
fi

IMAGE_TYPE="others"

if [ "$SOURCE_IMAGE_TYPE" = "centos" ]; then
   SOURCE_IMAGE_TAG=$CENTOS_VERSION
elif [ "$SOURCE_IMAGE_TYPE" = "rhel" ]; then
   SOURCE_IMAGE_TAG=$RHEL_VERSION
   IMAGE_NAME="$IMAGE_NAME-$SOURCE_IMAGE_TYPE"
elif [ "$SOURCE_IMAGE_TYPE" = "ubi" ]; then
   SOURCE_IMAGE_TAG=$UBI_VERSION
elif [ "$SOURCE_IMAGE_TYPE" = "ubim" ]; then
   if [ -n "$UBIM_SHA" ]; then
     # We need to use the SHA
     SOURCE_IMAGE_TAG=$UBIM_SHA
   else
     SOURCE_IMAGE_TAG=$UBIM_VERSION
   fi
   IMAGE_TYPE="ubim"
elif [ "$SOURCE_IMAGE_TYPE" = "ubimicro" ]; then
   # if using ubimicro, download common CSM ubimicro image
   curl -O -L https://raw.githubusercontent.com/dell/csm/main/config/csm-common.mk
   local_path=$(pwd)
   source $local_path/csm-common.mk
   CSM_COMMON_BASE_IMAGE=$DEFAULT_BASEIMAGE
   echo "Using CSM common image for ubimicro: ${CSM_COMMON_BASE_IMAGE}"
   IMAGE_TYPE="ubimicro"
fi

build_source_image_repo_name

if [ "$SOURCE_IMAGE_TYPE" = "ubimicro" ]; then
   echo "Adding driver dependencies to ubi micro image"
   bash ./buildubimicro.sh "$CSM_COMMON_BASE_IMAGE"
   SOURCE_REPO="localhost/csipowermax-ubimicro"
   SOURCE_IMAGE_TAG="latest"
fi 

if [ "$SOURCE_IMAGE_TYPE" = "ubim" ]; then
  if [ -n "$UBIM_SHA" ]; then
    SOURCE_REPO="$SOURCE_REPO@sha256"
  fi
fi

# Check if BUILD_NUMBER is set
if [ -n "$BUILD_NUMBER" ]; then
   printf -v BUILD_NUMBER_FROM_ENV "%03d" $BUILD_NUMBER
   echo "BUILD_NUMBER specified via environment and set to:" $BUILD_NUMBER_FROM_ENV
   BUILD_NUMBER=$BUILD_NUMBER_FROM_ENV
else
   BUILD_NUMBER=$TEMP_BUILD_NUMBER
fi
# Check if Build Type is set
if [ -n "$BUILD_TYPE" ]; then
   echo "BUILD_TYPE specified via environment and set to:" $BUILD_TYPE
else
   echo "BUILD_TYPE not specified. Defaulting to : 'X'"
   BUILD_TYPE="X"
fi

VERSION="$MAJOR_VERSION.$MINOR_VERSION.$PATCH_NUMBER.$BUILD_NUMBER$BUILD_TYPE"

# Build image tags
build_image_tags

# Build the image
build_image

if [ "$PUSH_IMAGE" = true ]; then
   echo "###################### PUSH IMAGE ############################"
   echo "IMAGE REPOSITORY is set to :" $BUILD_REPO
   if [ -n "$BUILD_REPO" ]; then
      [[ $NOPROMPT == "false" ]] && read -n 1 -p "Are you sure you want to push to the above image repository (y/n)? " YN && echo
      [[ $YN == "y" || $YN == "Y" || $YN == "" ]] && push_image
   else 
      echo "IMAGE REPOSITORY not set. Nothing to push to"
      exit 1
   fi
fi

if [ "$DELETE_IMAGE" = true ]; then
   echo "####################### DELETING LOCAL IMAGE #####################"
   IMAGE_ID=$($BUILDCMD images $IMAGE_VERSION_TAG --format "{{.ID}}")
   echo "IMAGE ID: "$IMAGE_ID
   $BUILDCMD images | grep $IMAGE_ID | awk '{print $1 ":" $2}' | xargs $BUILDCMD rmi
fi
