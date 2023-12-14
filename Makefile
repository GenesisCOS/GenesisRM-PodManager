GO_BIN := go 
OUTPUT = bin
REPO := emoslab.registry:6000
TAG := dev 

BUILD_TGTS = build_controller build_swiftlet

all: ${BUILD_TGTS} build_container 

build_container: ${BUILD_TGTS}
	docker build -t ${REPO}/swiftkube/swiftlet:${TAG} -f kubernetes/Dockerfile.swiftlet .
	docker build -t ${REPO}/swiftkube/swift-controller-manager:${TAG} -f kubernetes/Dockerfile.swift-controller-manager .

build_controller: ${OUTPUT}
	${GO_BIN} build -o ${OUTPUT}/swift-controller-manager cmd/swift-controller-manager/controller-manager.go

build_swiftlet: ${OUTPUT}
	${GO_BIN} build -o ${OUTPUT}/swiftlet cmd/swiftlet/swiftlet.go 

${OUTPUT}:
	@if test -d ${OUTPUT}; then  \
		echo "${OUTPUT} exist";  \
	else  \
		mkdir ${OUTPUT};  \
	fi 

push:
	docker push ${REPO}/swiftkube/swiftlet:${TAG}
	docker push ${REPO}/swiftkube/swift-controller-manager:${TAG}

stop:
	kubectl delete -f kubernetes/serviceaccount.yaml 
	kubectl delete -f kubernetes/swift-controller-manager.yaml 
	kubectl delete -f kubernetes/swiftlet.yaml 

start:
	kubectl apply -f kubernetes/serviceaccount.yaml 
	kubectl apply -f kubernetes/swift-controller-manager.yaml 
	kubectl apply -f kubernetes/swiftlet.yaml 

list:
	kubectl get pod -n kube-system 

.PHONY: clean 
clean:
	rm -rf bin 
	docker rmi ${REPO}/swiftkube/swiftlet:${TAG}
	docker rmi ${REPO}/swiftkube/swift-controller-manager:${TAG}
