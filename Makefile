GO_BIN := go 
OUTPUT = bin
REPO := registry:5000
TAG := dev 

BUILD_TGTS = build_controller build_swiftlet build_monitor 

all: ${BUILD_TGTS} build-image

build-image: ${BUILD_TGTS}
	docker build -t ${REPO}/swiftkube/swiftlet:${TAG} -f kubernetes/Dockerfile.swiftlet .
	docker build -t ${REPO}/swiftkube/swift-controller-manager:${TAG} -f kubernetes/Dockerfile.swift-controller-manager .
	docker build -t ${REPO}/swiftkube/swift-monitor:${TAG} -f kubernetes/Dockerfile.swift-monitor . 

build_controller: ${OUTPUT}
	${GO_BIN} build -o ${OUTPUT}/swift-controller-manager cmd/swift-controller-manager/controller-manager.go

build_swiftlet: ${OUTPUT}
	${GO_BIN} build -o ${OUTPUT}/swiftlet cmd/swiftlet/swiftlet.go 

build_monitor:
	${GO_BIN} build -o ${OUTPUT}/swift-monitor cmd/swift-monitor/monitor.go 

${OUTPUT}:
	@if test -d ${OUTPUT}; then  \
		echo "${OUTPUT} exist";  \
	else  \
		mkdir ${OUTPUT};  \
	fi 

push-image:
	docker push ${REPO}/swiftkube/swiftlet:${TAG}
	docker push ${REPO}/swiftkube/swift-controller-manager:${TAG}
	docker push ${REPO}/swiftkube/swift-monitor:${TAG}

uninstall_all:
	-kubectl delete -f kubernetes/serviceaccount.yaml 
	-kubectl delete -f kubernetes/swift-controller-manager.yaml 
	-kubectl delete -f kubernetes/swiftlet.yaml
	-kubectl delete -f kubernetes/swift-monitor.yaml  

create_serviceaccount:
	kubectl apply -f kubernetes/serviceaccount.yaml 

install_all: create_serviceaccount
	kubectl apply -f kubernetes/swift-controller-manager.yaml 
	kubectl apply -f kubernetes/swiftlet.yaml 
	kubectl apply -f kubernetes/swift-monitor.yaml 

install_monitor: create_serviceaccount
	kubectl apply -f kubernetes/swift-monitor.yaml 

.PHONY: clean 
clean:
	rm -rf bin 
	docker rmi ${REPO}/swiftkube/swiftlet:${TAG}
	docker rmi ${REPO}/swiftkube/swift-controller-manager:${TAG}
	docker rmi ${REPO}/swiftkube/swift-monitor:${TAG}
