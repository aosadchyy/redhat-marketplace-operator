// Copyright 2020 IBM Corp.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package meterdefinition

import (
	"context"
	"fmt"
	"strings"

	monitoringv1 "github.com/coreos/prometheus-operator/pkg/apis/monitoring/v1"
	v1alpha1 "github.com/redhat-marketplace/redhat-marketplace-operator/pkg/apis/marketplace/v1alpha1"
	"github.com/redhat-marketplace/redhat-marketplace-operator/pkg/config"
	"github.com/redhat-marketplace/redhat-marketplace-operator/pkg/meter_definition"
	"github.com/redhat-marketplace/redhat-marketplace-operator/pkg/prometheus"

	"github.com/redhat-marketplace/redhat-marketplace-operator/pkg/utils"
	"github.com/redhat-marketplace/redhat-marketplace-operator/pkg/utils/patch"
	. "github.com/redhat-marketplace/redhat-marketplace-operator/pkg/utils/reconcileutils"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"
)

const meterDefinitionFinalizer = "meterdefinition.finalizer.marketplace.redhat.com"

const (
	MeteredResourceAnnotationKey = "marketplace.redhat.com/meteredUIDs"
)

var (
	log = logf.Log.WithName("controller_meterdefinition")
	// uid to name and namespace
	store *meter_definition.MeterDefinitionStore

	saClient *prometheus.ServiceAccountClient
)

// Add creates a new MeterDefinition Controller and adds it to the Manager. The Manager will set fields on the Controller
// and Start it when the Manager is Started.
func Add(
	mgr manager.Manager,
	ccprovider ClientCommandRunnerProvider,
	kubernetesInterface kubernetes.Interface,
	cfg config.OperatorConfig,
) error {
	return add(mgr, newReconciler(mgr, ccprovider, kubernetesInterface, cfg))
}

// newReconciler returns a new reconcile.Reconciler
func newReconciler(mgr manager.Manager, ccprovider ClientCommandRunnerProvider, kubernetesInterface kubernetes.Interface, cfg config.OperatorConfig) reconcile.Reconciler {
	opts := &MeterDefOpts{}
	
	return &ReconcileMeterDefinition{
		client:               mgr.GetClient(),
		kubernetesInterface : kubernetesInterface,
		scheme:               mgr.GetScheme(),
		ccprovider:           ccprovider,
		opts:                 opts,
		patcher:              patch.RHMDefaultPatcher,
		cfg:                  cfg,
	}
}

// add adds a new Controller to mgr with r as the reconcile.Reconciler
func add(mgr manager.Manager, r reconcile.Reconciler) error {
	// Create a new controller
	c, err := controller.New("meterdefinition-controller", mgr, controller.Options{Reconciler: r})
	if err != nil {
		return err
	}

	// Watch for changes to primary resource MeterDefinition
	err = c.Watch(&source.Kind{Type: &v1alpha1.MeterDefinition{}}, &handler.EnqueueRequestForObject{})
	if err != nil {
		return err
	}

	return err
}

// blank assignment to verify that ReconcileMeterDefinition implements reconcile.Reconciler
var _ reconcile.Reconciler = &ReconcileMeterDefinition{}

// ReconcileMeterDefinition reconciles a MeterDefinition object
type ReconcileMeterDefinition struct {
	// This client, initialized using mgr.Client() above, is a split client
	// that reads objects from the cache and writes to the apiserver
	client               client.Client
	kubernetesInterface  kubernetes.Interface
	scheme               *runtime.Scheme
	ccprovider           ClientCommandRunnerProvider
	opts                 *MeterDefOpts
	patcher              patch.Patcher
	cfg                  config.OperatorConfig
}

type MeterDefOpts struct{}

// Reconcile reads that state of the cluster for a MeterDefinition object and makes changes based on the state read
// and what is in the MeterDefinition.Spec
func (r *ReconcileMeterDefinition) Reconcile(request reconcile.Request) (reconcile.Result, error) {
	reqLogger := log.WithValues("Request.Namespace", request.Namespace, "Request.Name", request.Name)

	reqLogger.Info("Reconciling MeterDefinition")

	cc := r.ccprovider.NewCommandRunner(r.client, r.scheme, reqLogger)

	// Fetch the MeterDefinition instance
	instance := &v1alpha1.MeterDefinition{}
	result, _ := cc.Do(context.TODO(), GetAction(request.NamespacedName, instance))

	if !result.Is(Continue) {
		if !result.Is(NotFound) {
			reqLogger.Info("MeterDef resource not found. Ignoring since object must be deleted.")
			return reconcile.Result{}, nil
		}

		if !result.Is(Error) {
			reqLogger.Error(result.GetError(), "Failed to get MeterDef.")
		}

		return result.Return()
	}

	reqLogger.Info("Found instance", "instance", instance.Name)

	var update bool

	if instance.Spec.ServiceMeterLabels != nil {
		instance.Spec.ServiceMeterLabels = nil
	}
	if instance.Spec.PodMeterLabels != nil {
		instance.Spec.PodMeterLabels = nil
	}

	switch {
	case instance.Status.Conditions.IsUnknownFor(v1alpha1.MeterDefConditionTypeHasResult):
		fallthrough
	case len(instance.Status.WorkloadResources) == 0:
		update = instance.Status.Conditions.SetCondition(v1alpha1.MeterDefConditionNoResults)
	case len(instance.Status.WorkloadResources) > 0:
		update = instance.Status.Conditions.SetCondition(v1alpha1.MeterDefConditionHasResults)
	}

	update = r.QueryPreview(cc,instance,request,reqLogger)

	result, _ = cc.Do(
		context.TODO(),
		Call(func() (ClientAction, error) {
			if !update {
				return nil, nil
			}

			return UpdateAction(instance, UpdateStatusOnly(true)), nil
		}),
	)
	if result.Is(Error) {
		reqLogger.Error(result.GetError(), "Failed to update status.")
	}

	reqLogger.Info("meterdef_preview","requeue rate",r.cfg.ControllerReconcileSettings.MeterDefControllerRequeueRate)

	reqLogger.Info("finished reconciling")
	return reconcile.Result{RequeueAfter: r.cfg.ControllerReconcileSettings.MeterDefControllerRequeueRate}, nil
}

func (r *ReconcileMeterDefinition) finalizeMeterDefinition(req *v1alpha1.MeterDefinition) (reconcile.Result, error) {
	var err error

	// TODO: add finalizers

	req.SetFinalizers(utils.RemoveKey(req.GetFinalizers(), meterDefinitionFinalizer))
	err = r.client.Update(context.TODO(), req)
	if err != nil {
		return reconcile.Result{}, err
	}
	return reconcile.Result{}, nil
}

// addFinalizer adds finalizers to the MeterDefinition CR
func (r *ReconcileMeterDefinition) addFinalizer(instance *v1alpha1.MeterDefinition) error {
	log.Info("Adding Finalizer to %s/%s", instance.Name, instance.Namespace)
	instance.SetFinalizers(append(instance.GetFinalizers(), meterDefinitionFinalizer))

	err := r.client.Update(context.TODO(), instance)
	if err != nil {
		log.Error(err, "Failed to update RazeeDeployment with the Finalizer %s/%s", instance.Name, instance.Namespace)
		return err
	}
	return nil
}

func labelsForServiceMonitor(name, namespace string) map[string]string {
	return map[string]string{
		"marketplace.redhat.com/metered":                  "true",
		"marketplace.redhat.com/deployed":                 "true",
		"marketplace.redhat.com/metered.kind":             "ServiceMonitor",
		"marketplace.redhat.com/serviceMonitor.Name":      name,
		"marketplace.redhat.com/serviceMonitor.Namespace": namespace,
	}
}

func labelsForKubeStateMonitor(name, namespace string) map[string]string {
	return map[string]string{
		"marketplace.redhat.com/metered":                   "true",
		"marketplace.redhat.com/deployed":                  "true",
		"marketplace.redhat.com/metered.kind":              "ServiceMonitor",
		"marketplace.redhat.com/meterDefinition.namespace": namespace,
		"marketplace.redhat.com/meterDefinition.name":      name,
	}
}

func makeRelabelConfig(source []string, action, target string) *monitoringv1.RelabelConfig {
	return &monitoringv1.RelabelConfig{
		SourceLabels: source,
		TargetLabel:  target,
		Action:       action,
	}
}

func makeRelabelReplaceConfig(source []string, target, regex, replacement string) *monitoringv1.RelabelConfig {
	return &monitoringv1.RelabelConfig{
		SourceLabels: source,
		TargetLabel:  target,
		Action:       "replace",
		Regex:        regex,
		Replacement:  replacement,
	}
}

func makeRelabelKeepConfig(source []string, regex string) *monitoringv1.RelabelConfig {
	return &monitoringv1.RelabelConfig{
		SourceLabels: source,
		Action:       "keep",
		Regex:        regex,
	}

}
func labelsToRegex(labels []string) string {
	return fmt.Sprintf("(%s)", strings.Join(labels, "|"))
}
