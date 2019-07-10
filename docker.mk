# Includes the following generated file to get semantic version information
include semver.mk
ifdef NOTES
	RELNOTE="-$(NOTES)"
else
	RELNOTE=
endif

VERSION="v$(MAJOR).$(MINOR).$(PATCH).$(BUILD)$(TYPE)"
REGISTRY="vmadurprd01.cec.lab.emc.com:5000/vmax-docker-snapshots/vmax-docker/csi-plugin/csi-powermax"

docker:
	docker build -t "$(REGISTRY):$(VERSION)" .

push:   
	docker push "$(REGISTRY):$(VERSION)"

version:
	@echo "MAJOR $(MAJOR) MINOR $(MINOR) PATCH $(PATCH) BUILD ${BUILD} TYPE ${TYPE} RELNOTE $(RELNOTE) SEMVER $(SEMVER)"
	@echo "Target Version: $(VERSION)"
