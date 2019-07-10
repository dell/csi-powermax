package k8integration

import (
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"

	"golang.org/x/crypto/ssh"
	//	types github.com/dell/csi-powermax/test/k8_integration/
)

//Defination of constants
const (
	Csiprovisioner = "csi-powermax"
)

// createStorageClassJSONFile will create JSON interface for SC
func createStorageClassJSONFile(newVolumeCreateRequest *VolumeCreateRequest, storageClassJSONFileName string, path string) error {
	sc := StorageClass{
		Apiversion: "storage.k8s.io/v1",
		Kind:       "StorageClass",
		Metadata: MetadataSC{
			Name: strings.ToLower(newVolumeCreateRequest.ServiceLevel) + "-sc",
		},
		Provisioner:   Csiprovisioner,
		ReclaimPolicy: "Delete",
		Parameters: ParametersSC{
			Symid:        newVolumeCreateRequest.SYMID,
			Srp:          newVolumeCreateRequest.SRP,
			ServiceLevel: newVolumeCreateRequest.ServiceLevel,
		},
	}

	err := jsonMarshal(sc, storageClassJSONFileName, path)
	return err
}

//createPVCJSONFile  will create JSON interface for PVC
func createPVCJSONFile(newVolumeCreateRequest *VolumeCreateRequest, PVCJSONFileName string, path string) error {

	for i := 0; i < len(newVolumeCreateRequest.VolumeNames); i++ {
		VolumeName := newVolumeCreateRequest.VolumeNames[i]

		pvc := PVC{
			Apiversion: "v1",
			Kind:       "PersistentVolumeClaim",
			Metadata: MetadataPVC{
				Name: VolumeName,
			},
			Spec: SpecPVC{
				AccessModes: []string{newVolumeCreateRequest.AccessModes},
				Resources: ResourcesPVC{
					Requests: RequestsPVC{
						Storage: newVolumeCreateRequest.VolSize,
					},
				},
				StorageClassName: strings.ToLower(newVolumeCreateRequest.ServiceLevel) + "-sc",
			},
		}

		err := jsonMarshal(pvc, PVCJSONFileName, path)
		if err != nil {
			return err
		}

	}

	return nil
}

//sshFiles will copy files from windows host to target host
func sshFiles(path string) error {
	fileInfo, err := ioutil.ReadDir(path)
	if err != nil {
		log.Fatal(err)
		return err
	}
	for _, f := range fileInfo {
		fmt.Println(f.Name())
		filename := f.Name()
		rfilename := path + filename
		file, _ := os.Open(rfilename)
		defer file.Close()
		stat, _ := file.Stat()
		MasterHostIP := os.Getenv("KUBERNETESMASTERIP")
		Username := os.Getenv("USERNAME")
		Password := os.Getenv("PASSWORD")
		if MasterHostIP == "" || Username == "" || Password == "" {
			log.Fatal(err)
			return err

		}
		sshConfig := &ssh.ClientConfig{
			User: Username,
			Auth: []ssh.AuthMethod{
				ssh.Password(Password),
			},
		}
		hostIP := MasterHostIP + ":22"
		sshConfig.HostKeyCallback = ssh.InsecureIgnoreHostKey()
		client, err := ssh.Dial("tcp", hostIP, sshConfig)
		if err != nil {
			fmt.Println(err)
			return err
		}
		session, _ := client.NewSession()
		defer session.Close()
		wg := sync.WaitGroup{}
		wg.Add(1)

		go func() {
			hostIn, _ := session.StdinPipe()
			defer hostIn.Close()
			fmt.Fprintf(hostIn, "C0664 %d %s\n", stat.Size(), filename)
			io.Copy(hostIn, file)
			fmt.Fprint(hostIn, "\x00")
			wg.Done()

		}()
		listDir := "ls /root/DirForDynamicJSONFiles"
		text1, err := executeKubernetesCommand(listDir)
		if strings.Contains(string(text1), "No such file or directory") {
			dirCreate := "mkdir /root/DirForDynamicJSONFiles/"
			_, err := executeKubernetesCommand(dirCreate)
			if err != nil {
				fmt.Println(err)
				return err
			}
		}
		session.Run("/usr/bin/scp -r -t /root/DirForDynamicJSONFiles")
		if err != nil {
			fmt.Println(err)
			return err
		}
	}
	return nil
}

// removeFiles will delete JSON files at last
func removeFiles(path string) error {

	d, err := os.Open(path)
	if err != nil {
		log.Fatal(err)
		return err
	}
	defer d.Close()
	names, err := d.Readdirnames(-1)
	if err != nil {
		log.Fatal(err)
		return err
	}
	for _, name := range names {
		err = os.RemoveAll(filepath.Join(path, name))
		if err != nil {
			log.Fatal(err)
			return err
		}
	}
	return nil
}

//jsonMarshal creates json and writes to a file from the interface.
func jsonMarshal(v interface{}, JSONFileName string, path string) error {
	data, err := json.MarshalIndent(v, "", "\t")
	if err != nil {
		fmt.Println(err)
		return err
	}
	file, err := os.OpenFile(path+JSONFileName, os.O_APPEND|os.O_CREATE, 0644)
	if err != nil {
		fmt.Println(err)
		return err
	}
	_, err = file.Write(data)
	if err != nil {
		fmt.Println(err)
		file.Close()
		return err
	}
	err = file.Close()
	if err != nil {
		fmt.Println(err)
		return err
	}
	return err

}

// CreateJSONFiles will generate json files dynamically
func CreateJSONFiles(newVolumeCreateRequest *VolumeCreateRequest, storageClassJSONFileName string, PVCJSONFileName string) error {
	var path string
	jsondir := "jsonFilesDir"
	if runtime.GOOS == "windows" {
		fmt.Println("Windows OS detected")
		wd, err := os.Getwd()
		if err != nil {
			return err
		}
		parent := filepath.Dir(wd)
		path = parent + "\\" + jsondir + "\\"
		if _, err := os.Stat(path); os.IsNotExist(err) {
			os.MkdirAll(path, 755)
		}
	} else if runtime.GOOS == "linux" {
		fmt.Println("Unix/Linux type OS detected")
		wd, err := os.Executable()
		if err != nil {
			log.Println(err)
			return err
		}
		parent := filepath.Dir(wd)
		path = parent + "/" + jsondir + "/"
		if _, err := os.Stat(path); os.IsNotExist(err) {
			os.MkdirAll(path, 755)
		}
	} else if runtime.GOOS == "darwin" {
		fmt.Println("Mac type OS detected")
		wd, err := os.Getwd()
		if err != nil {
			log.Println(err)
			return err
		}
		parent := filepath.Dir(wd)
		path = parent + "/" + jsondir + "/"
		if _, err := os.Stat(path); os.IsNotExist(err) {
			os.MkdirAll(path, 755)
		}
	}

	err := createStorageClassJSONFile(newVolumeCreateRequest, storageClassJSONFileName, path)
	if err != nil {
		fmt.Println("Failed to create the StorageClassJSON File")
		return err
	}

	pvcerr := createPVCJSONFile(newVolumeCreateRequest, PVCJSONFileName, path)
	if pvcerr != nil {
		fmt.Println("Failed to create the PVCJSON File")
		return pvcerr
	}

	ssherr := sshFiles(path)
	if ssherr != nil {
		fmt.Println("Failed to transfer files to  target host")
		return ssherr
	}

	delerr := removeFiles(path)
	if delerr != nil {
		fmt.Println("Failed to clean up the files.")
		return delerr
	}

	return nil

}
