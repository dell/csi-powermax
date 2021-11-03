/*
 Copyright © 2021 Dell Inc. or its subsidiaries. All Rights Reserved.

 Licensed under the Apache License, Version 2.0 (the "License");
 you may not use this file except in compliance with the License.
 You may obtain a copy of the License at
      http://www.apache.org/licenses/LICENSE-2.0
 Unless required by applicable law or agreed to in writing, software
 distributed under the License is distributed on an "AS IS" BASIS,
 WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 See the License for the specific language governing permissions and
 limitations under the License.
*/

package k8smock

import (
	"fmt"
	"os"
	"revproxy/v2/pkg/utils"
	"testing"

	log "github.com/sirupsen/logrus"
)

func TestMain(m *testing.M) {
	status := 0
	if st := m.Run(); st > status {
		status = st
	}
	err := utils.RemoveTempFiles()
	if err != nil {
		log.Fatalf("Failed to cleanup temp files. (%s)\n", err.Error())
		status = 1
	}
	os.Exit(status)
}

func TestInit(t *testing.T) {
	k8sUtils := Init()
	fmt.Printf("mockUtils: %+v\n", k8sUtils)
}

func TestGetCertFileFromSecretName(t *testing.T) {
	k8sUtils := Init()
	// Create a new secret.
	secret, err := k8sUtils.CreateNewCertSecret("test-cert-secret-name")
	if err != nil {
		t.Errorf("Failed to create cert secret. (%s)\n", err.Error())
		return
	}
	// Get cert file from the newly created secret.
	certFile, err := k8sUtils.GetCertFileFromSecretName(secret.Name)
	if err != nil {
		t.Errorf("Failed to get the certfile from secret. (%s)\n", err.Error())
		return
	}
	fmt.Printf("Cert file name = %s\n", certFile)
}

func TestGetCertFileFromSecret(t *testing.T) {
	k8sUtils := Init()
	// Create a new secret.
	secret, err := k8sUtils.CreateNewCertSecret("test-cert-secret")
	if err != nil {
		t.Errorf("Failed to create cert secret. (%s)\n", err.Error())
		return
	}
	certFile, err := k8sUtils.GetCertFileFromSecret(secret)
	if err != nil {
		t.Errorf("Failed to get the certfile from secret. (%s)\n", err.Error())
		return
	}
	fmt.Printf("Cert file name = %s\n", certFile)
}

func TestGetCredentialsFromSecret(t *testing.T) {
	k8sUtils := Init()
	secret, err := k8sUtils.CreateNewCredentialSecret("test-credential-secret")
	if err != nil {
		t.Errorf("Failed to create creds secret. (%s)\n", err.Error())
		return
	}
	credentials, err := k8sUtils.GetCredentialsFromSecret(secret)
	if err != nil {
		t.Errorf("Failed to get the credentials from secret. (%s)\n", err.Error())
		return
	}
	fmt.Printf("Username: %s, Password: %s\n", credentials.UserName, credentials.Password)
}

func TestGetCredentialsFromSecretName(t *testing.T) {
	k8sUtils := Init()
	secret, err := k8sUtils.CreateNewCredentialSecret("test-credential-secret-name")
	if err != nil {
		t.Errorf("Failed to create creds secret. (%s)\n", err.Error())
		return
	}
	credentials, err := k8sUtils.GetCredentialsFromSecretName(secret.Name)
	if err != nil {
		return
	}
	fmt.Printf("Username: %s, Password: %s\n", credentials.UserName, credentials.Password)
}
