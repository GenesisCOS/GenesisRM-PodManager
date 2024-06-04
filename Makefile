GO_BIN := go 
OUTPUT = bin
REPO := registry:5000
TAG := dev 

BUILD_TGTS = build_appmanager build_podmanager build_ctl build_agent 

all: ${BUILD_TGTS} build-image

build-image: ${BUILD_TGTS}
	docker build -t ${REPO}/swiftkube/swiftlet:${TAG} -f kubernetes/Dockerfile.swiftlet .
	docker build -t ${REPO}/swiftkube/swift-controller-manager:${TAG} -f kubernetes/Dockerfile.swift-controller-manager .
	docker build -t ${REPO}/swiftkube/swift-monitor:${TAG} -f kubernetes/Dockerfile.swift-monitor . 

build_binary: ${BUILD_TGTS}

build_agent: ${OUTPUT}
	${GO_BIN} build \
		-ldflags "-X k8s.io/component-base/version.gitVersion=v1.27.2-genesis" \
		-o ${OUTPUT}/genesis-agent cmd/genesis-agent/main.go

setup_agent:
	ansible myhosts -i ansible.ini -m copy -a "src=${OUTPUT}/genesis-agent dest=/usr/local/bin/genesis-agent"
	ansible myhosts -i ansible.ini -m shell -a "chmod +x /usr/local/bin/genesis-agent"

build_proxy: ${OUTPUT}
	${GO_BIN} build \
		-ldflags "-X k8s.io/component-base/version.gitVersion=v1.27.2-genesis" \
		-o ${OUTPUT}/genesis-proxy cmd/genesis-proxy/main.go

setup_proxy:
	ansible myhosts -i ansible.ini -m copy -a "src=${OUTPUT}/genesis-proxy dest=/usr/local/bin/genesis-proxy"
	ansible myhosts -i ansible.ini -m shell -a "chmod +x /usr/local/bin/genesis-proxy"

build_ctl: ${OUTPUT}
	${GO_BIN} build -o ${OUTPUT}/genesisctl cmd/genesisctl/main.go 

install_ctl: 
	install -m0700 ${OUTPUT}/genesisctl /usr/local/bin

build_appmanager: ${OUTPUT} 
	${GO_BIN} build -o ${OUTPUT}/appmanager cmd/appmanager/main.go

#build_swiftlet: ${OUTPUT}
#	${GO_BIN} build -o ${OUTPUT}/swiftlet cmd/swiftlet/swiftlet.go 

build_podmanager:
	${GO_BIN} build -o ${OUTPUT}/podmanager cmd/podmanager/main.go 

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

push_podmanager:
	ansible myhosts -i ansible.ini -m copy -a "src=${OUTPUT}/podmanager dest=/usr/local/bin/podmanager"
	ansible myhosts -i ansible.ini -m shell -a "chmod +x /usr/local/bin/podmanager"

setup_podmanager:
	ansible myhosts -i ansible.ini -m shell -a "tmux new-session -s podmanager -d 'podmanager'"

resetup_podmanager: build_podmanager 
	-ansible myhosts -i ansible.ini -m shell -a "tmux kill-session -t podmanager"
	ansible myhosts -i ansible.ini -m copy -a "src=${OUTPUT}/podmanager dest=/usr/local/bin/podmanager"
	ansible myhosts -i ansible.ini -m shell -a "chmod +x /usr/local/bin/podmanager"
	ansible myhosts -i ansible.ini -m shell -a "tmux new-session -s podmanager -d 'podmanager'"

resetup_podmanager_916: build_podmanager 
	-ansible kunpeng-916 -i ansible.ini -m shell -a "tmux kill-session -t podmanager"
	ansible kunpeng-916 -i ansible.ini -m copy -a "src=${OUTPUT}/podmanager dest=/usr/local/bin/podmanager"
	ansible kunpeng-916 -i ansible.ini -m shell -a "chmod +x /usr/local/bin/podmanager && tmux new-session -s podmanager -d 'podmanager'"

stop_podmanager:
	ansible myhosts -i ansible.ini -m shell -a "tmux kill-session -t podmanager"

setup_appmanager:
	cp bin/appmanager /usr/local/bin/appmanager 
	tmux new-session -s appmanager -d 'appmanager'

stop_appmanager:
	tmux kill-session -t appmanager 

resetup_appmanager: build_appmanager
	- tmux kill-session -t appmanager 
	cp bin/appmanager /usr/local/bin/appmanager 
	tmux new-session -s appmanager -d 'appmanager'

.PHONY: clean 
clean:
	rm -rf bin 
	docker rmi ${REPO}/swiftkube/swiftlet:${TAG}
	docker rmi ${REPO}/swiftkube/swift-controller-manager:${TAG}
	docker rmi ${REPO}/swiftkube/swift-monitor:${TAG}
