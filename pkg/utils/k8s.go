package utils

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/ioutil"

	"github.com/gotidy/ptr"
	"github.com/imdario/mergo"
	corev1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	k8yaml "k8s.io/apimachinery/pkg/util/yaml"
	"sigs.k8s.io/controller-runtime/pkg/client"

	monitoringv1 "github.com/coreos/prometheus-operator/pkg/apis/monitoring/v1"
	opsrcv1 "github.com/operator-framework/operator-marketplace/pkg/apis/operators/v1"
	marketplacev1alpha1 "github.ibm.com/symposium/redhat-marketplace-operator/pkg/apis/marketplace/v1alpha1"
)

type PersistentVolume struct {
	*metav1.ObjectMeta
	StorageClass *string
	StorageSize  *resource.Quantity
	AccessMode   *corev1.PersistentVolumeAccessMode
}

func NewPersistentVolumeClaim(values PersistentVolume) (corev1.PersistentVolumeClaim, error) {
	// set some defaults
	quantity := resource.MustParse("20Gi")
	accessMode := corev1.ReadWriteOnce
	defaults := PersistentVolume{
		ObjectMeta:   &metav1.ObjectMeta{},
		StorageClass: ptr.String(""),
		AccessMode:   &accessMode,
		StorageSize:  &quantity,
	}

	// merge values from pv into values
	if err := mergo.Merge(&values, defaults); err != nil {
		return corev1.PersistentVolumeClaim{}, err
	}

	return corev1.PersistentVolumeClaim{
		ObjectMeta: *values.ObjectMeta,
		Spec: corev1.PersistentVolumeClaimSpec{
			AccessModes: []corev1.PersistentVolumeAccessMode{
				*values.AccessMode,
			},
			Resources: corev1.ResourceRequirements{
				Requests: corev1.ResourceList{
					"storage": *values.StorageSize,
				},
			},
			StorageClassName: values.StorageClass,
		},
	}, nil
}

// GetPodNames returns the pod names of the array of pods passed in
func GetPodNames(pods []corev1.Pod) []string {
	var podNames []string
	for _, pod := range pods {
		podNames = append(podNames, pod.Name)
	}
	return podNames
}

// GetDefaultStorageClass attempts to return the default storage class
// of the cluster and errors if it cannot be found
func GetDefaultStorageClass(client client.Client) (string, error) {
	storageList := &storagev1.StorageClassList{}

	if err := client.List(context.TODO(), storageList); err != nil {
		return "", err
	}

	defaultStorageOptions := []string{}

	for _, storageClass := range storageList.Items {
		if storageClass.Annotations["storageclass.kubernetes.io/is-default-class"] == "true" {
			defaultStorageOptions = append(defaultStorageOptions, storageClass.Name)
		}
	}

	if len(defaultStorageOptions) == 0 {
		return "", fmt.Errorf("could not find a default storage class")
	}

	if len(defaultStorageOptions) > 1 {
		return "", fmt.Errorf("multiple default options, cannot pick one")
	}

	return defaultStorageOptions[0], nil
}

// MakeProbe creates a probe with the specified path and prot
func MakeProbe(path string, port, initialDelaySeconds, timeoutSeconds int32) *corev1.Probe {
	return &corev1.Probe{
		Handler: corev1.Handler{
			HTTPGet: &corev1.HTTPGetAction{
				Path: path,
				Port: intstr.FromInt(int(port)),
			},
		},
		InitialDelaySeconds: initialDelaySeconds,
		TimeoutSeconds:      timeoutSeconds,
	}
}

// BuildNewOpSrc returns a new Operator Source
func BuildNewOpSrc() *opsrcv1.OperatorSource {
	opsrc := &opsrcv1.OperatorSource{
		ObjectMeta: metav1.ObjectMeta{
			Name: OPSRC_NAME,
			// Must always be openshift-marketplace
			Namespace: OPERATOR_MKTPLACE_NS,
		},
		Spec: opsrcv1.OperatorSourceSpec{
			DisplayName:       "Red Hat Marketplace",
			Endpoint:          "https://quay.io/cnr",
			Publisher:         "Red Hat Marketplace",
			RegistryNamespace: "redhat-marketplace",
			Type:              "appregistry",
		},
	}

	return opsrc
}

// BuildRazeeCrd returns a RazeeDeployment cr with default values
func BuildRazeeCr(namespace, clusterUUID string, deploySecretName *string) *marketplacev1alpha1.RazeeDeployment {

	cr := &marketplacev1alpha1.RazeeDeployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      RAZEE_NAME,
			Namespace: namespace,
		},
		Spec: marketplacev1alpha1.RazeeDeploymentSpec{
			Enabled:          true,
			ClusterUUID:      clusterUUID,
			DeploySecretName: deploySecretName,
		},
	}

	return cr
}

// BuildMeterBaseCr returns a MeterBase cr with default values
func BuildMeterBaseCr(namespace string) *marketplacev1alpha1.MeterBase {

	cr := &marketplacev1alpha1.MeterBase{
		ObjectMeta: metav1.ObjectMeta{
			Name:      METERBASE_NAME,
			Namespace: namespace,
		},
		Spec: marketplacev1alpha1.MeterBaseSpec{
			Enabled: true,
			Prometheus: &marketplacev1alpha1.PrometheusSpec{
				Storage: marketplacev1alpha1.StorageSpec{
					Size: resource.MustParse("20Gi"),
				},
			},
		},
	}
	return cr
}

func LoadYAML(filename string, i interface{}) (interface{}, error) {
	dat, err := ioutil.ReadFile(filename)
	if err != nil {
		return nil, err
	}

	dec := k8yaml.NewYAMLOrJSONDecoder(bytes.NewReader(dat), 1000)
	var genericTypeVal interface{}

	switch v := i.(type) {
	case corev1.ConfigMap:
		genericTypeVal = &corev1.ConfigMap{}
	case monitoringv1.Prometheus:
		genericTypeVal = &monitoringv1.Prometheus{}
	default:
		return nil, fmt.Errorf("type not recognized %T", v)
	}

	if err := dec.Decode(&genericTypeVal); err != nil {
		return nil, err
	}

	return genericTypeVal, nil
}

// filterByNamespace returns a ResourceList of Pods and ServiceMonitors filtered by namespaces
func FilterByNamespace(namespaces []corev1.Namespace, resources corev1.ResourceList, rClient client.Client) (error, corev1.ResourceList) {
	var err error
	if len(namespaces) == 0 {
		// if no namespaces are passed, return resources across all namespaces
		listOpts := []client.ListOption{
			client.InNamespace(""),
		}
		err, resources = getResources(listOpts, resources, rClient)

	} else if len(namespaces) == 1 {
		//if passed a single namespace, return resources across that namespace
		listOpts := []client.ListOption{
			client.InNamespace(namespaces[0].ObjectMeta.Name),
		}
		err, resources = getResources(listOpts, resources, rClient)

	} else if len(namespaces) > 1 {
		//if more than one namespaces is passed, loop through and add all resources to the ResourceList
		for _, ns := range namespaces {
			listOpts := []client.ListOption{
				client.InNamespace(ns.ObjectMeta.Name),
			}
			err, resources = getResources(listOpts, resources, rClient)
		}
	} else {
		err = errors.New("unexpected length of []namespaces")
	}
	return err, resources
}

// getResources() is a helper function for FilterByNamespace(), it returns a ResourceList in the requested namespaces
// the namespaces are preset in listOpts
func getResources(listOpts []client.ListOption, resources corev1.ResourceList, rClient client.Client) (error, corev1.ResourceList) {

	// Return resources for type Pod
	podList := &corev1.PodList{}
	err := rClient.List(context.TODO(), podList, listOpts...)
	if err != nil {
		return err, resources
	}
	for _, pod := range podList.Items {
		resources[corev1.ResourceName(pod.GetName())] = resource.MustParse("1")
	}
	// Return resoruces for type ServiceMonitor
	serviceMonitorList := &monitoringv1.ServiceMonitorList{}
	err = rClient.List(context.TODO(), serviceMonitorList, listOpts...)
	if err != nil {
		return err, resources
	}
	for _, serviceMonitor := range serviceMonitorList.Items {
		resources[corev1.ResourceName(serviceMonitor.GetName())] = resource.MustParse("1")
	}

	return err, resources
}
