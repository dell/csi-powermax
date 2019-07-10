# Dell EMC Powermax Helm Chart for Kubernetes

For detailed installation instructions, please check the doc directory

The general outline is:

    1. Satisfy the pre-requsites outlined in the Release and Installation Notes in the doc directory.

    2. Create a Kubernetes secret with the PowerMax credentials using the template in secret.yaml.

    3. Copy the `csi-powermax/values.yaml` to a file  `myvalues.yaml` in this directory and fill in various installation parameters.

    4. Invoke the `install.powermax` shell script which deploys the helm chart in csi-powermax.