// Code generated by Wire. DO NOT EDIT.

//go:generate wire
//+build !wireinject

package inject

import (
	"github.com/redhat-marketplace/redhat-marketplace-operator/v2/pkg/config"
	"github.com/redhat-marketplace/redhat-marketplace-operator/v2/pkg/managers"
	"github.com/redhat-marketplace/redhat-marketplace-operator/v2/pkg/runnables"
	"github.com/redhat-marketplace/redhat-marketplace-operator/v2/pkg/utils/reconcileutils"
	"k8s.io/client-go/kubernetes"
)

// Injectors from wire.go:

func initializeRunnables(fields *managers.ControllerFields, namespace managers.DeployedNamespace) (runnables.Runnables, error) {
	logger := fields.Logger
	config := fields.Config
	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		return nil, err
	}
	client := fields.Client
	scheme := fields.Scheme
	clientCommandRunner := reconcileutils.NewClientCommand(client, scheme, logger)
	podMonitorConfig := managers.ProvidePodMonitorConfig(namespace)
	podMonitor := runnables.NewPodMonitor(logger, clientset, clientCommandRunner, podMonitorConfig)
	runnablesRunnables := runnables.ProvideRunnables(podMonitor)
	return runnablesRunnables, nil
}

func initializeInjectables(fields *managers.ControllerFields, namespace managers.DeployedNamespace) (Injectables, error) {
	client := fields.Client
	scheme := fields.Scheme
	logger := fields.Logger
	clientCommandRunner := reconcileutils.NewClientCommand(client, scheme, logger)
	clientCommandInjector := &ClientCommandInjector{
		Fields:        fields,
		CommandRunner: clientCommandRunner,
	}
	operatorConfigInjector := &OperatorConfigInjector{}
	patchInjector := &PatchInjector{}
	operatorConfig, err := config.ProvideConfig()
	if err != nil {
		return nil, err
	}
	factoryInjector := &FactoryInjector{
		Fields:    fields,
		Config:    operatorConfig,
		Namespace: namespace,
	}
	injectables := ProvideInjectables(clientCommandInjector, operatorConfigInjector, patchInjector, factoryInjector)
	return injectables, nil
}

func initializeCommandRunner(fields *managers.ControllerFields) (reconcileutils.ClientCommandRunner, error) {
	client := fields.Client
	scheme := fields.Scheme
	logger := fields.Logger
	clientCommandRunner := reconcileutils.NewClientCommand(client, scheme, logger)
	return clientCommandRunner, nil
}