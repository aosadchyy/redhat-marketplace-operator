package meter_definition

import (
	"context"
	"sync"
	"time"

	monitoringv1 "github.com/coreos/prometheus-operator/pkg/apis/monitoring/v1"
	monitoringv1client "github.com/coreos/prometheus-operator/pkg/client/versioned/typed/monitoring/v1"
	"github.com/go-logr/logr"
	"github.com/redhat-marketplace/redhat-marketplace-operator/pkg/apis/marketplace/v1alpha1"
	rhmclient "github.com/redhat-marketplace/redhat-marketplace-operator/pkg/client"
	marketplacev1alpha1client "github.com/redhat-marketplace/redhat-marketplace-operator/pkg/generated/clientset/versioned/typed/marketplace/v1alpha1"
	. "github.com/redhat-marketplace/redhat-marketplace-operator/pkg/utils/reconcileutils"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	clientset "k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"
)

type ObjectUID types.UID
type MeterDefUID types.UID
type ResourceSet map[MeterDefUID]*v1alpha1.WorkloadResource
type ObjectResourceMessageAction string

const (
	AddMessageAction    ObjectResourceMessageAction = "Add"
	DeleteMessageAction                             = "Delete"
)

type ObjectResourceMessage struct {
	Action ObjectResourceMessageAction
	Object interface{}
	*ObjectResourceValue
}

type ObjectResourceKey struct {
	ObjectUID
	MeterDefUID
}

func NewObjectResourceKey(object metav1.Object, meterdefUID MeterDefUID) ObjectResourceKey {
	return ObjectResourceKey{
		ObjectUID:   ObjectUID(object.GetUID()),
		MeterDefUID: meterdefUID,
	}
}

type ObjectResourceValue struct {
	MeterDef     types.NamespacedName
	MeterDefHash string
	Generation   int64
	Matched      bool
	*v1alpha1.WorkloadResource
}

func NewObjectResourceValue(
	lookup *MeterDefinitionLookupFilter,
	resource *v1alpha1.WorkloadResource,
	obj metav1.Object,
	matched bool,
) *ObjectResourceValue {
	return &ObjectResourceValue{
		MeterDef:         lookup.MeterDefName,
		MeterDefHash:     lookup.Hash(),
		WorkloadResource: resource,
		Generation:       obj.GetGeneration(),
		Matched:          matched,
	}
}

// MeterDefinitionStore keeps the MeterDefinitions in place
// and tracks the dependents using the rules based on the
// rules. MeterDefinition controller uses this to effectively
// find the child assets of a meter definition rules.
type MeterDefinitionStore struct {
	meterDefinitionFilters map[MeterDefUID]*MeterDefinitionLookupFilter
	objectResourceSet      map[ObjectResourceKey]*ObjectResourceValue

	mutex sync.RWMutex

	ctx context.Context
	log logr.Logger

	cc ClientCommandRunner

	namespaces []string

	kubeClient        clientset.Interface
	findOwner         *rhmclient.FindOwnerHelper
	monitoringClient  *monitoringv1client.MonitoringV1Client
	marketplaceClient *marketplacev1alpha1client.MarketplaceV1alpha1Client

	listeners []chan *ObjectResourceMessage
}

func NewMeterDefinitionStore(
	ctx context.Context,
	log logr.Logger,
	cc ClientCommandRunner,
	kubeClient clientset.Interface,
	findOwner *rhmclient.FindOwnerHelper,
	monitoringClient *monitoringv1client.MonitoringV1Client,
	marketplaceclient *marketplacev1alpha1client.MarketplaceV1alpha1Client,
) *MeterDefinitionStore {
	return &MeterDefinitionStore{
		ctx:                    ctx,
		log:                    log,
		cc:                     cc,
		kubeClient:             kubeClient,
		monitoringClient:       monitoringClient,
		marketplaceClient:      marketplaceclient,
		findOwner:              findOwner,
		listeners:              []chan *ObjectResourceMessage{},
		meterDefinitionFilters: make(map[MeterDefUID]*MeterDefinitionLookupFilter),
		objectResourceSet:      make(map[ObjectResourceKey]*ObjectResourceValue),
	}
}

func (s *MeterDefinitionStore) RegisterListener(ch chan *ObjectResourceMessage) {
	s.listeners = append(s.listeners, ch)
}

func (s *MeterDefinitionStore) addMeterDefinition(meterdef *v1alpha1.MeterDefinition, lookup *MeterDefinitionLookupFilter) {
	s.meterDefinitionFilters[MeterDefUID(meterdef.UID)] = lookup
}

func (s *MeterDefinitionStore) removeMeterDefinition(meterdef *v1alpha1.MeterDefinition) {
	delete(s.meterDefinitionFilters, MeterDefUID(meterdef.UID))
	for key := range s.objectResourceSet {
		if key.MeterDefUID == MeterDefUID(meterdef.GetUID()) {
			delete(s.objectResourceSet, key)
		}
	}
}

func (s *MeterDefinitionStore) broadcast(msg *ObjectResourceMessage) {
	for _, ch := range s.listeners {
		ch <- msg
	}
}

var (
	meterDefGVK, _ = meta.TypeAccessor(&v1alpha1.MeterDefinition{})
)

func (s *MeterDefinitionStore) GetMeterDefinitionRefs(uid types.UID) []*ObjectResourceValue {
	s.mutex.RLock()
	defer s.mutex.RUnlock()

	vals := []*ObjectResourceValue{}
	for key, val := range s.objectResourceSet {
		if key.ObjectUID == ObjectUID(uid) && val.Matched {
			vals = append(vals, val)
		}
	}
	return vals
}

// Implementing k8s.io/client-go/tools/cache.Store interface

// Add inserts adds to the OwnerCache by calling the metrics generator functions and
// adding the generated metrics to the metrics map that underlies the MetricStore.
func (s *MeterDefinitionStore) Add(obj interface{}) error {

	if meterdef, ok := obj.(*v1alpha1.MeterDefinition); ok {
		lookup, err := NewMeterDefinitionLookupFilter(s.cc, meterdef, s.findOwner)

		if err != nil {
			s.log.Error(err, "error building lookup")
			return err
		}

		s.log.Info("found lookup", "lookup", lookup)
		s.meterDefinitionFilters[MeterDefUID(meterdef.UID)] = lookup
		return nil
	}

	o, err := meta.Accessor(obj)
	if err != nil {
		return err
	}

	// look over all meterDefinitions, matching workloads are saved
	for meterDefUID, lookup := range s.meterDefinitionFilters {
		key := NewObjectResourceKey(o, meterDefUID)
		s.mutex.RLock()
		previousResult, ok := s.objectResourceSet[key]
		s.mutex.RUnlock()

		if ok && previousResult.MeterDefHash == lookup.Hash() &&
			o.GetGeneration() == previousResult.Generation {
			// no change in the lookup, result would not have changed
			break
		}

		workload, ok, err := lookup.FindMatchingWorkloads(obj)

		if err != nil {
			s.log.Error(err, "")
			return err
		}

		var msg *ObjectResourceMessage
		err = func() error {
			s.mutex.Lock()
			defer s.mutex.Unlock()

			if !ok {
				value := NewObjectResourceValue(lookup, nil, o, ok)
				s.objectResourceSet[key] = value
				return nil
			}

			resource, err := v1alpha1.NewWorkloadResource(*workload, obj)
			if err != nil {
				s.log.Error(err, "")
				return err
			}

			value := NewObjectResourceValue(lookup, resource, o, ok)
			s.objectResourceSet[key] = value

			msg = &ObjectResourceMessage{
				Action:              AddMessageAction,
				Object:              obj,
				ObjectResourceValue: value,
			}

			return nil
		}()

		if err != nil {
			return err
		}

		if msg != nil {
			s.broadcast(msg)
		}

	}

	return nil
}

// Update updates the existing entry in the OwnerCache.
func (s *MeterDefinitionStore) Update(obj interface{}) error {
	// TODO: For now, just call Add, in the future one could check if the resource version changed?
	return s.Add(obj)
}

// Delete deletes an existing entry in the OwnerCache.
func (s *MeterDefinitionStore) Delete(obj interface{}) error {
	s.mutex.Lock()
	defer s.mutex.Unlock()

	if meterdef, ok := obj.(*v1alpha1.MeterDefinition); ok {
		s.removeMeterDefinition(meterdef)
		return nil
	}

	o, err := meta.Accessor(obj)
	if err != nil {
		return err
	}

	s.broadcast(&ObjectResourceMessage{
		Action:              DeleteMessageAction,
		Object:              o,
		ObjectResourceValue: nil,
	})

	for key := range s.objectResourceSet {
		if key.ObjectUID == ObjectUID(o.GetUID()) {
			delete(s.objectResourceSet, key)
		}
	}

	return nil
}

// List implements the List method of the store interface.
func (s *MeterDefinitionStore) List() []interface{} {
	return nil
}

// ListKeys implements the ListKeys method of the store interface.
func (s *MeterDefinitionStore) ListKeys() []string {
	return nil
}

// Get implements the Get method of the store interface.
func (s *MeterDefinitionStore) Get(obj interface{}) (item interface{}, exists bool, err error) {
	return nil, false, nil
}

// GetByKey implements the GetByKey method of the store interface.
func (s *MeterDefinitionStore) GetByKey(key string) (item interface{}, exists bool, err error) {
	return nil, false, nil
}

// Replace will delete the contents of the store, using instead the
// given list.
func (s *MeterDefinitionStore) Replace(list []interface{}, _ string) error {
	s.mutex.Lock()
	s.objectResourceSet = make(map[ObjectResourceKey]*ObjectResourceValue)
	s.mutex.Unlock()

	for _, o := range list {
		s.broadcast(&ObjectResourceMessage{
			Action:              DeleteMessageAction,
			Object:              o,
			ObjectResourceValue: nil,
		})

		err := s.Add(o)
		if err != nil {
			return err
		}
	}

	return nil
}

// Resync implements the Resync method of the store interface.
func (s *MeterDefinitionStore) Resync() error {
	return nil
}

func (s *MeterDefinitionStore) Start() {
	for _, ns := range s.namespaces {
		for expectedType, lister := range s.createWatchers(ns) {
			reflector := cache.NewReflector(lister, expectedType, s, 0)
			go reflector.Run(s.ctx.Done())
		}
	}
	go func() {
		ticker := time.NewTicker(30 * time.Second)
		for {
			select {
			case <-ticker.C:
				func() {
					s.mutex.RLock()
					defer s.mutex.RUnlock()

					s.log.Info("current state",
						"meterDefs", s.meterDefinitionFilters)
				}()
			case <-s.ctx.Done():
				return
			}
		}
	}()
}

func (s *MeterDefinitionStore) SetNamespaces(ns []string) {
	s.namespaces = ns
}

func (s *MeterDefinitionStore) createWatchers(ns string) map[runtime.Object]cache.ListerWatcher {
	return map[runtime.Object]cache.ListerWatcher{
		&corev1.PersistentVolumeClaim{}: CreatePVCListWatch(s.kubeClient, ns),
		&corev1.Pod{}:                   CreatePodListWatch(s.kubeClient, ns),
		&corev1.Service{}:               CreateServiceListWatch(s.kubeClient, ns),
		&monitoringv1.ServiceMonitor{}:  CreateServiceMonitorListWatch(s.monitoringClient, ns),
		&v1alpha1.MeterDefinition{}:     CreateMeterDefinitionWatch(s.marketplaceClient, ns),
		//CreateServiceListWatch(s.kubeClient, ns),
	}
}
