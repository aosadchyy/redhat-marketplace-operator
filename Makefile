SHELL:=/bin/bash
NAMESPACE ?= redhat-marketplace-operator
OPSRC_NAMESPACE = marketplace-operator
OPERATOR_SOURCE = redhat-marketplace-operators
IMAGE_REGISTRY ?= public-image-registry.apps-crc.testing/symposium
OPERATOR_IMAGE_NAME ?= redhat-marketplace-operator
OPERATOR_IMAGE_TAG ?= latest
VERSION ?= $(shell go run scripts/version/main.go)

SERVICE_ACCOUNT := redhat-marketplace-operator
SECRETS_NAME := my-docker-secrets

OPERATOR_IMAGE := $(IMAGE_REGISTRY)/$(OPERATOR_IMAGE_NAME):$(OPERATOR_IMAGE_TAG)

.DEFAULT_GOAL := help

##@ Application

install: ## Install all resources (CR/CRD's, RBAC and Operator)
	@echo ....... Creating namespace .......
	- kubectl create namespace ${NAMESPACE}
	@echo ....... Creating CRDs .......
	- kubectl create -f deploy/crds/marketplace.redhat.com_marketplaceconfigs_crd.yaml --namespace=${NAMESPACE}
	- kubectl create -f deploy/crds/marketplace.redhat.com_meterbases_crd.yaml --namespace=${NAMESPACE}
	- kubectl create -f deploy/crds/marketplace.redhat.com_meterings_crd.yaml --namespace=${NAMESPACE}
	- kubectl create -f deploy/crds/marketplace.redhat.com_razeedeployments_crd.yaml --namespace=${NAMESPACE}
	@echo ....... Applying serivce accounts and role ........
	- kubectl apply -f deploy/role.yaml --namespace=${NAMESPACE}
	- kubectl apply -f deploy/role_binding.yaml --namespace=${NAMESPACE}
	- kubectl apply -f deploy/service_account.yaml --namespace=${NAMESPACE}
	@echo ....... Applying Operator .......
	- kubectl apply -f deploy/operator.yaml --namespace=${NAMESPACE}
	@echo ....... Applying Rules and Service Account .......
	- kubectl apply -f deploy/crds/marketplace.redhat.com_v1alpha1_marketplaceconfig_cr.yaml --namespace=${NAMESPACE}
	- kubectl apply -f deploy/crds/marketplace.redhat.com_v1alpha1_meterbase_cr.yaml --namespace=${NAMESPACE}
	- kubectl apply -f deploy/crds/marketplace.redhat.com_v1alpha1_razeedeployment_cr.yaml --namespace=${NAMESPACE}
	- kubectl apply -f deploy/crds/marketplace.redhat.com_v1alpha1_metering_cr.yaml --namespace=${NAMESPACE}

uninstall: ## Uninstall all that all performed in the $ make install
	@echo ....... Uninstalling .......
	@echo ....... Deleting CRDs.......
	- kubectl delete -f deploy/crds/marketplace.redhat.com_marketplaceconfigs_crd.yaml --namespace=${NAMESPACE}
	- kubectl delete -f deploy/crds/marketplace.redhat.com_meterbases_crd.yaml --namespace=${NAMESPACE}
	- kubectl delete -f deploy/crds/marketplace.redhat.com_meterings_crd.yaml --namespace=${NAMESPACE}
	- kubectl delete -f deploy/crds/marketplace.redhat.com_razeedeployments_crd.yaml --namespace=${NAMESPACE}
	@echo ....... Deleting Rules and Service Account .......
	- kubectl delete -f deploy/role.yaml --namespace=${NAMESPACE}
	- kubectl delete -f deploy/role_binding.yaml --namespace=${NAMESPACE}
	- kubectl delete -f deploy/service_account.yaml --namespace=${NAMESPACE}
	@echo ....... Deleting Operator .......
	- kubectl delete opsrc ${OPERATOR_SOURCE} --namespace=${OPSRC_NAMESPACE}
	- kubectl delete -f deploy/operator.yaml --namespace=${NAMESPACE}
	@echo ....... Deleting namespace ${NAMESPACE}.......
	- kubectl delete namespace ${NAMESPACE}

##@ Build

.PHONY: build
build: ## Build the operator executable
	@echo Adding assets
	@mkdir -p build/_output
	- [ -d "build/_output/assets" ] && rm -rf build/_output/assets
	- [ -f "build/_output/bin/redhat-marketplace-operator" ] && rm -f build/_output/bin/redhat-marketplace-operator
	@cp -r ./assets build/_output
	GOOS=linux GOARCH=amd64 go build -o build/_output/bin/redhat-marketplace-operator ./cmd/manager/main.go
	docker build . -f ./build/Dockerfile -t $(OPERATOR_IMAGE)

.PHONY: push
push: push ## Push the operator image
	docker push $(OPERATOR_IMAGE)

generate-csv: ## Generate the csv
	operator-sdk generate csv --csv-version $(VERSION) --csv-config=./deploy/olm-catalog/csv-config.yaml --update-crds

docker-login: ## Log into docker using env $DOCKER_USER and $DOCKER_PASSWORD
	@docker login -u="$(DOCKER_USER)" -p="$(DOCKER_PASSWORD)" quay.io

##@ Development

setup: ## Setup minikube for full operator dev
	@echo Applying prometheus operator
	kubectl apply -f https://raw.githubusercontent.com/coreos/prometheus-operator/master/bundle.yaml
	@echo Applying olm
	kubectl apply -f https://raw.githubusercontent.com/operator-framework/operator-lifecycle-manager/master/deploy/upstream/quickstart/crds.yaml
	kubectl apply -f https://raw.githubusercontent.com/operator-framework/operator-lifecycle-manager/master/deploy/upstream/quickstart/olm.yaml
	@echo Applying operator marketplace
	for item in 01_namespace.yaml 02_catalogsourceconfig.crd.yaml 03_operatorsource.crd.yaml 04_service_account.yaml 05_role.yaml 06_role_binding.yaml 07_upstream_operatorsource.cr.yaml 08_operator.yaml ; do \
		kubectl apply -f https://raw.githubusercontent.com/operator-framework/operator-marketplace/master/deploy/upstream/$$item ; \
	done

code-vet: ## Run go vet for this project. More info: https://golang.org/cmd/vet/
	@echo go vet
	go vet $$(go list ./... )

code-fmt: ## Run go fmt for this project
	@echo go fmt
	go fmt $$(go list ./... )

code-templates: ## Gen templates
	@RELATED_IMAGE_MARKETPLACE_OPERATOR=$(OPERATOR_IMAGE) NAMESPACE=$(NAMESPACE) scripts/gen_files.sh

code-dev: ## Run the default dev commands which are the go fmt and vet then execute the $ make code-gen
	@echo Running the common required commands for developments purposes
	- make code-fmt
	- make code-vet
	- make code-gen

code-gen: ## Run the operator-sdk commands to generated code (k8s and crds)
	@echo Updating the deep copy files with the changes in the API
	operator-sdk generate k8s
	@echo Updating the CRD files with the OpenAPI validations
	operator-sdk generate crds
	@echo Generating the yamls for deployment
	- make code-templates


##@ Manual Testing

create: ##creates the required crds for this deployment
	@echo creating crds
	- kubectl create namespace ${NAMESPACE}
	- kubectl create -f deploy/crds/marketplace.redhat.com_marketplaceconfigs_crd.yaml --namespace=${NAMESPACE}
	- kubectl create -f deploy/crds/marketplace.redhat.com_razeedeployments_crd.yaml --namespace=${NAMESPACE}
	- kubectl create -f deploy/crds/marketplace.redhat.com_meterings_crd.yaml --namespace=${NAMESPACE}
	- kubectl create -f deploy/crds/marketplace.redhat.com_meterbases_crd.yaml --namespace=${NAMESPACE}

deploys: ##deploys the resources for deployment
	@echo deploying services and operators
	- kubectl create -f deploy/service_account.yaml --namespace=${NAMESPACE}
	- kubectl create -f deploy/role.yaml --namespace=${NAMESPACE}
	- kubectl create -f deploy/role_binding.yaml --namespace=${NAMESPACE}
	- kubectl create -f deploy/operator.yaml --namespace=${NAMESPACE}

apply: ##applies changes to crds
	- kubectl apply -f deploy/crds/marketplace.redhat.com_v1alpha1_marketplaceconfig_cr.yaml --namespace=${NAMESPACE}

clean: ##delete the contents created in 'make create'
	@echo deleting resources
	- kubectl delete opsrc ${OPERATOR_SOURCE} --namespace=${OPSRC_NAMESPACE}
	- kubectl delete -f deploy/crds/marketplace.redhat.com_v1alpha1_marketplaceconfig_cr.yaml --namespace=${NAMESPACE}
	- kubectl delete -f deploy/crds/marketplace.redhat.com_v1alpha1_razeedeployment_cr.yaml --namespace=${NAMESPACE}
	- kubectl delete -f deploy/crds/marketplace.redhat.com_v1alpha1_metering_cr.yaml --namespace=${NAMESPACE}
	- kubectl delete -f deploy/crds/marketplace.redhat.com_v1alpha1_meterbase_cr.yaml --namespace=${NAMESPACE}
	- kubectl delete -f deploy/operator.yaml --namespace=${NAMESPACE}
	- kubectl delete -f deploy/role_binding.yaml --namespace=${NAMESPACE}
	- kubectl delete -f deploy/role.yaml --namespace=${NAMESPACE}
	- kubectl delete -f deploy/service_account.yaml --namespace=${NAMESPACE}
	- kubectl delete -f deploy/crds/marketplace.redhat.com_marketplaceconfigs_crd.yaml --namespace=${NAMESPACE}
	- kubectl delete -f deploy/crds/marketplace.redhat.com_razeedeployments_crd.yaml --namespace=${NAMESPACE}
	- kubectl delete -f deploy/crds/marketplace.redhat.com_meterings_crd.yaml --namespace=${NAMESPACE}
	- kubectl delete -f deploy/crds/marketplace.redhat.com_meterbases_crd.yaml --namespace=${NAMESPACE}

delete-razee: ##delete the razee CR
	@echo deleting razee CR
	- kubectl delete -f  deploy/crds/marketplace.redhat.com_v1alpha1_razeedeployment_cr.yaml -n ${NAMESPACE}

##@ Tests

.PHONY: test
test: ## Run go tests
	@echo ... Run tests
	go test ./...

.PHONY: test-cover
test-cover: ## Run coverage on code
	@echo Running coverage
	go test -coverprofile cover.out ./...
	go tool cover -func=cover.out

.PHONY: test-e2e
test-e2e: ## Run integration e2e tests with different options.
	@echo ... Making build for e2e ...
	@echo ... Applying code templates for e2e ...
	- make code-templates
	@echo ... Running the same e2e tests with different args ...
	@echo ... Running locally ...
	- kubectl create namespace ${NAMESPACE} || true
	- operator-sdk test local ./test/e2e --namespace=${NAMESPACE} --go-test-flags="-tags e2e"

##@ Help

.PHONY: help
help: ## Display this help
	@echo -e "Usage:\n  make \033[36m<target>\033[0m"
	@echo Targets:
	@awk 'BEGIN {FS = ":.*##"}; \
		/^[a-zA-Z0-9_-]+:.*?##/ { printf "  \033[36m%-15s\033[0m %s\n", $$1, $$2 } \
		/^##@/ { printf "\n\033[1m%s\033[0m\n", substr($$0, 5) } ' $(MAKEFILE_LIST)
