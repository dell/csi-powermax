package service

import (
	"context"
	"testing"

	"github.com/dell/csi-powermax/v2/pkg/symmetrix/mocks"
	types "github.com/dell/gopowermax/v2/types/v100"
	"github.com/container-storage-interface/spec/lib/go/csi"
	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"
)

func TestCreateVolumeSelectionProfile(t *testing.T) {
	tests := []struct {
		name               string
		isFile             bool
		contentSource      *csi.VolumeContentSource
		replicationEnabled bool
		repMode            string
		isThick            bool
		expected           backendSelectionProfile
	}{
		{
			name:               "block volume no content source no replication",
			isFile:             false,
			contentSource:      nil,
			replicationEnabled: false,
			repMode:            "",
			isThick:            false,
			expected: backendSelectionProfile{
				IsFile:             false,
				HasContentSource:   false,
				ReplicationEnabled: false,
				ReplicationMode:    "",
				IsThick:            false,
			},
		},
		{
			name:               "block volume with content source",
			isFile:             false,
			contentSource:      &csi.VolumeContentSource{Type: &csi.VolumeContentSource_Snapshot{}},
			replicationEnabled: false,
			repMode:            "",
			isThick:            false,
			expected: backendSelectionProfile{
				IsFile:             false,
				HasContentSource:   true,
				ReplicationEnabled: false,
				ReplicationMode:    "",
				IsThick:            false,
			},
		},
		{
			name:               "file volume replication",
			isFile:             true,
			contentSource:      nil,
			replicationEnabled: true,
			repMode:            "METRO",
			isThick:            false,
			expected: backendSelectionProfile{
				IsFile:             true,
				HasContentSource:   false,
				ReplicationEnabled: true,
				ReplicationMode:    "METRO",
				IsThick:            false,
			},
		},
		{
			name:               "thick volume",
			isFile:             false,
			contentSource:      nil,
			replicationEnabled: false,
			repMode:            "",
			isThick:            true,
			expected: backendSelectionProfile{
				IsFile:             false,
				HasContentSource:   false,
				ReplicationEnabled: false,
				ReplicationMode:    "",
				IsThick:            true,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			profile := createVolumeSelectionProfile(tt.isFile, tt.contentSource, tt.replicationEnabled, tt.repMode, tt.isThick)
			assert.Equal(t, tt.expected, profile)
		})
	}
}

func TestPublishVolumeSelectionProfile(t *testing.T) {
	tests := []struct {
		name        string
		isFile      bool
		remoteSymID string
		expected    backendSelectionProfile
	}{
		{
			name:        "block volume not replicated",
			isFile:      false,
			remoteSymID: "",
			expected: backendSelectionProfile{
				IsFile:             false,
				HasContentSource:   false,
				ReplicationEnabled: false,
				ReplicationMode:    "",
			},
		},
		{
			name:        "block volume replicated",
			isFile:      false,
			remoteSymID: "000123456789",
			expected: backendSelectionProfile{
				IsFile:             false,
				HasContentSource:   false,
				ReplicationEnabled: true,
				ReplicationMode:    "",
			},
		},
		{
			name:        "file volume",
			isFile:      true,
			remoteSymID: "",
			expected: backendSelectionProfile{
				IsFile:             true,
				HasContentSource:   false,
				ReplicationEnabled: false,
				ReplicationMode:    "",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			profile := publishVolumeSelectionProfile(tt.isFile, tt.remoteSymID)
			assert.Equal(t, tt.expected, profile)
		})
	}
}

func TestSelectBackendPath(t *testing.T) {
	empty := backendSelectionProfile{}
	assert.Equal(t, backendLegacy, selectBackendPath(empty, "10.4.0.3"))
	assert.Equal(t, backendU4P104, selectBackendPath(empty, "10.4.0.4"))
	assert.Equal(t, backendU4P104, selectBackendPath(empty, "10.4.1.0"))
	assert.Equal(t, backendLegacy, selectBackendPath(empty, ""))
}

func TestSelectBackendPath_ForcesLegacyForNFS(t *testing.T) {
	nfs := backendSelectionProfile{IsFile: true}
	assert.Equal(t, backendLegacy, selectBackendPath(nfs, "10.4.0.4"))
	assert.Equal(t, backendLegacy, selectBackendPath(nfs, "99.0.0.0"))
}

func TestSelectBackendPath_ForcesLegacyForReplication(t *testing.T) {
	repl := backendSelectionProfile{ReplicationEnabled: true}
	assert.Equal(t, backendLegacy, selectBackendPath(repl, "10.4.0.4"))
	assert.Equal(t, backendLegacy, selectBackendPath(repl, "99.0.0.0"))
}

func TestCreatorFor_SelectsU4P104CreatorWhenVersionIs104OrHigher(t *testing.T) {
	path, creator := creatorFor(&service{}, nil, nil, "000000000001", "req-1", map[string]string{}, false, "10.4.0.4", APIVersion104, nil, nil)
	assert.Equal(t, backendU4P104, path)
	assert.IsType(t, &u4p104VolumeCreator{}, creator)
}

func TestCreatorFor_SelectsLegacyCreatorWhenVersionIsLessThan104OrUnknown(t *testing.T) {
	path, creator := creatorFor(&service{}, nil, nil, "000000000001", "req-1", map[string]string{}, false, "10.4.0.3", 103, nil, nil)
	assert.Equal(t, backendLegacy, path)
	assert.NotNil(t, creator)

	path, creator = creatorFor(&service{}, nil, nil, "000000000001", "req-1", map[string]string{}, false, "", 0, nil, nil)
	assert.Equal(t, backendLegacy, path)
	assert.NotNil(t, creator)
}

func TestCreatorFor_SelectsLegacyForNFS(t *testing.T) {
	nfsCaps := []*csi.VolumeCapability{{
		AccessType: &csi.VolumeCapability_Mount{Mount: &csi.VolumeCapability_MountVolume{FsType: NFS}},
	}}
	path, creator := creatorFor(&service{}, nil, nil, "000000000001", "req-1", map[string]string{}, false, "10.4.0.4", APIVersion104, nfsCaps, nil)
	assert.Equal(t, backendLegacy, path)
	assert.IsType(t, &legacyVolumeCreator{}, creator)
}

func TestCreatorFor_SelectsLegacyForReplication(t *testing.T) {
	params := map[string]string{RepEnabledParam: "true"}
	path, creator := creatorFor(&service{}, nil, nil, "000000000001", "req-1", params, false, "10.4.0.4", APIVersion104, nil, nil)
	assert.Equal(t, backendLegacy, path)
	assert.IsType(t, &legacyVolumeCreator{}, creator)
}

func TestPublisherFor_SelectsU4P104PublisherWhenVersionIs104OrHigher(t *testing.T) {
	vc := &csi.VolumeCapability{
		AccessType: &csi.VolumeCapability_Mount{Mount: &csi.VolumeCapability_MountVolume{FsType: "ext4"}},
	}
	publisher := publisherFor(context.Background(), &service{}, nil, nil, "000000000001", "00001", "vol-id", "vol-name", "", "", "req-1", "10.4.0.4", vc)
	assert.IsType(t, &u4p104VolumePublisher{}, publisher)
}

func TestPublisherFor_SelectsLegacyPublisherWhenVersionIsLessThan104OrUnknown(t *testing.T) {
	publisher := publisherFor(context.Background(), &service{}, nil, nil, "000000000001", "00001", "vol-id", "vol-name", "", "", "req-1", "10.4.0.3", nil)
	assert.IsType(t, &legacyVolumePublisher{}, publisher)

	publisher = publisherFor(context.Background(), &service{}, nil, nil, "000000000001", "00001", "vol-id", "vol-name", "", "", "req-1", "", nil)
	assert.IsType(t, &legacyVolumePublisher{}, publisher)
}

func TestPublisherFor_SelectsLegacyForNFS(t *testing.T) {
	nfsCap := &csi.VolumeCapability{
		AccessType: &csi.VolumeCapability_Mount{Mount: &csi.VolumeCapability_MountVolume{FsType: NFS}},
	}
	publisher := publisherFor(context.Background(), &service{}, nil, nil, "000000000001", "00001", "vol-id", "vol-name", "", "", "req-1", "10.4.0.4", nfsCap)
	assert.IsType(t, &legacyVolumePublisher{}, publisher)
}

func TestPublisherFor_SelectsLegacyForReplication(t *testing.T) {
	publisher := publisherFor(context.Background(), &service{}, nil, nil, "000000000001", "00001", "vol-id", "vol-name", "000120000002", "011BC", "req-1", "10.4.0.4", nil)
	assert.IsType(t, &legacyVolumePublisher{}, publisher)
}

func TestPublisherFor_SelectsLegacyForStaticFileSystem(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	mockClient := mocks.NewMockPmaxClient(ctrl)
	// GetFileSystemByID returns success → file system exists → should select legacy
	mockClient.EXPECT().GetFileSystemByID(gomock.Any(), "000120000001", "011AB").
		Return(&types.FileSystem{}, nil).Times(1)
	// fsType is empty → triggers static FS check
	vc := &csi.VolumeCapability{
		AccessType: &csi.VolumeCapability_Mount{Mount: &csi.VolumeCapability_MountVolume{FsType: ""}},
	}
	publisher := publisherFor(context.Background(), &service{}, mockClient, nil, "000120000001", "011AB", "vol-id", "vol-name", "", "", "req-1", "10.4.0.4", vc)
	assert.IsType(t, &legacyVolumePublisher{}, publisher)
}

func TestPublisherFor_InjectsDependenciesIntoLegacyPublisher(t *testing.T) {
	svc := &service{}
	publisher := publisherFor(context.Background(), svc, nil, nil, "SYM001", "DEV01", "vol-1", "myVol", "RSYM", "RVOL", "req-42", "10.4.0.3", nil)
	lp, ok := publisher.(*legacyVolumePublisher)
	assert.True(t, ok)
	assert.Equal(t, svc, lp.s)
	assert.Equal(t, "SYM001", lp.symID)
	assert.Equal(t, "DEV01", lp.devID)
	assert.Equal(t, "vol-1", lp.volID)
	assert.Equal(t, "myVol", lp.volumeName)
	assert.Equal(t, "RSYM", lp.remoteSymID)
	assert.Equal(t, "RVOL", lp.remoteVolumeID)
	assert.Equal(t, "req-42", lp.reqID)
}

func TestPublisherFor_InjectsDependenciesIntoU4P104Publisher(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	svc := &service{}
	mockClient := mocks.NewMockPmaxClient(ctrl)
	mockClient104 := mocks.NewMockPmaxClient(ctrl)
	vc := &csi.VolumeCapability{
		AccessType: &csi.VolumeCapability_Mount{Mount: &csi.VolumeCapability_MountVolume{FsType: "ext4"}},
	}
	publisher := publisherFor(context.Background(), svc, mockClient, mockClient104, "SYM001", "DEV01", "vol-1", "myVol", "", "", "req-42", "10.4.0.4", vc)
	up, ok := publisher.(*u4p104VolumePublisher)
	assert.True(t, ok)
	assert.Equal(t, svc, up.s)
	assert.Equal(t, mockClient, up.legacyClient)
	assert.Equal(t, mockClient104, up.client104)
	assert.Equal(t, "SYM001", up.symID)
	assert.Equal(t, "DEV01", up.devID)
	assert.Equal(t, "vol-1", up.volID)
	assert.Equal(t, "myVol", up.volumeName)
	assert.Equal(t, "", up.remoteSymID)
	assert.Equal(t, "", up.remoteVolumeID)
	assert.Equal(t, "req-42", up.reqID)
}

func TestSelectBackendPath_ForcesLegacyForThickVolumes(t *testing.T) {
	thick := backendSelectionProfile{IsThick: true}
	assert.Equal(t, backendLegacy, selectBackendPath(thick, "10.4.0.4"))
	assert.Equal(t, backendLegacy, selectBackendPath(thick, "99.0.0.0"))
}

func TestCreatorFor_SelectsLegacyForThickVolumes(t *testing.T) {
	params := map[string]string{ThickVolumesParam: "true"}
	path, creator := creatorFor(&service{}, nil, nil, "000000000001", "req-1", params, false, "10.4.0.4", 104, nil, nil)
	assert.Equal(t, backendLegacy, path)
	assert.IsType(t, &legacyVolumeCreator{}, creator)
}

func TestCreatorFor_SelectsU4P104WhenThickIsFalseOrAbsent(t *testing.T) {
	// ThickVolumesParam explicitly false
	params := map[string]string{ThickVolumesParam: "false"}
	path, creator := creatorFor(&service{}, nil, nil, "000000000001", "req-1", params, false, "10.4.0.4", 104, nil, nil)
	assert.Equal(t, backendU4P104, path)
	assert.IsType(t, &u4p104VolumeCreator{}, creator)

	// ThickVolumesParam absent
	path2, creator2 := creatorFor(&service{}, nil, nil, "000000000001", "req-1", map[string]string{}, false, "10.4.0.4", 104, nil, nil)
	assert.Equal(t, backendU4P104, path2)
	assert.IsType(t, &u4p104VolumeCreator{}, creator2)
}

func TestPublisherFor_SelectsLegacyForVSphere(t *testing.T) {
	svc := &service{}
	svc.opts.IsVsphereEnabled = true
	vc := &csi.VolumeCapability{
		AccessType: &csi.VolumeCapability_Mount{Mount: &csi.VolumeCapability_MountVolume{FsType: "ext4"}},
	}
	publisher := publisherFor(context.Background(), svc, nil, nil, "000000000001", "00001", "vol-id", "vol-name", "", "", "req-1", "10.4.0.4", vc)
	assert.IsType(t, &legacyVolumePublisher{}, publisher)
}

func TestPublisherFor_SelectsU4P104WhenVSphereDisabled(t *testing.T) {
	svc := &service{}
	svc.opts.IsVsphereEnabled = false
	vc := &csi.VolumeCapability{
		AccessType: &csi.VolumeCapability_Mount{Mount: &csi.VolumeCapability_MountVolume{FsType: "ext4"}},
	}
	publisher := publisherFor(context.Background(), svc, nil, nil, "000000000001", "00001", "vol-id", "vol-name", "", "", "req-1", "10.4.0.4", vc)
	assert.IsType(t, &u4p104VolumePublisher{}, publisher)
}

func TestParseUnisphereVersion(t *testing.T) {
	tests := []struct {
		name     string
		version  string
		expected []int
		ok       bool
	}{
		{name: "prefixed version", version: "V10.4.0.4", expected: []int{10, 4, 0, 4}, ok: true},
		{name: "non-prefixed version", version: "10.4.0.4", expected: []int{10, 4, 0, 4}, ok: true},
		{name: "extra components", version: "10.4.0.4.99", expected: []int{10, 4, 0, 4, 99}, ok: true},
		{name: "empty version", version: "", expected: nil, ok: false},
		{name: "malformed version", version: "10.4.x.4", expected: nil, ok: false},
		{name: "too short version", version: "10.4.0", expected: nil, ok: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			actual, ok := parseUnisphereVersion(tt.version)
			assert.Equal(t, tt.ok, ok)
			assert.Equal(t, tt.expected, actual)
		})
	}
}

func TestIsVersionAtLeast(t *testing.T) {
	minimum := []int{10, 4, 0, 4}
	assert.True(t, isVersionAtLeast("10.4.0.4", minimum))
	assert.True(t, isVersionAtLeast("V10.4.1.0", minimum))
	assert.False(t, isVersionAtLeast("10.4.0.3", minimum))
	assert.False(t, isVersionAtLeast("", minimum))
	assert.False(t, isVersionAtLeast("10.4.bad.4", minimum))
}
