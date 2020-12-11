package clusterregistration

import (
	"context"
	"errors"
	"reflect"

	"github.com/google/uuid"
	openshiftconfigv1 "github.com/openshift/api/config/v1"
	marketplacev1alpha1 "github.com/redhat-marketplace/redhat-marketplace-operator/pkg/apis/marketplace/v1alpha1"
	"github.com/redhat-marketplace/redhat-marketplace-operator/pkg/config"
	"github.com/redhat-marketplace/redhat-marketplace-operator/pkg/marketplace"
	"github.com/redhat-marketplace/redhat-marketplace-operator/pkg/utils"
	v1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"
)

var log = logf.Log.WithName("controller_registration_watcher")

// Add creates a new ClusterRegistration Controller and adds it to the Manager. The Manager will set fields on the Controller
// and Start it when the Manager is Started.
func Add(mgr manager.Manager) error {
	return add(mgr, newReconciler(mgr))
}

// newReconciler returns a new reconcile.Reconciler
func newReconciler(mgr manager.Manager) reconcile.Reconciler {
	return &ReconcileClusterRegistration{client: mgr.GetClient(), scheme: mgr.GetScheme()}
}

// blank assignment to verify that ReconcileClusterRegistration implements reconcile.Reconciler
var _ reconcile.Reconciler = &ReconcileClusterRegistration{}

// ReconcileClusterRegistration reconciles a Registration object
type ReconcileClusterRegistration struct {
	// This client, initialized using mgr.Client() above, is a split client
	// that reads objects from the cache and writes to the apiserver
	client client.Client
	scheme *runtime.Scheme
}

func add(mgr manager.Manager, r reconcile.Reconciler) error {
	// Create a new controller
	c, err := controller.New("clusterregistration-controller", mgr, controller.Options{Reconciler: r})
	if err != nil {
		return err
	}

	// Watch for secret created with name 'redhat-marketplace-pull-secret'
	namePredicate := predicate.Funcs{
		CreateFunc: func(e event.CreateEvent) bool {
			secret, ok := e.Object.(*v1.Secret)
			if !ok {
				return false
			}
			secretName := secret.ObjectMeta.Name
			if _, ok := secret.Data[utils.RHMPullSecretKey]; ok && secretName == utils.RHMPullSecretName {
				return true
			}
			return false
		},
		UpdateFunc: func(e event.UpdateEvent) bool {
			secret, ok := e.ObjectNew.(*v1.Secret)
			if !ok {
				return false
			}
			if _, ok := secret.Data[utils.RHMPullSecretKey]; !ok {
				return false
			}
			return e.ObjectOld != e.ObjectNew
		},
		DeleteFunc: func(event.DeleteEvent) bool {
			return false
		},
		GenericFunc: func(e event.GenericEvent) bool {
			secret, ok := e.Object.(*v1.Secret)
			if !ok {
				return false
			}
			secretName := secret.ObjectMeta.Name
			if _, ok := secret.Data[utils.RHMPullSecretKey]; ok && secretName == utils.RHMPullSecretName {
				return true
			}
			return false
		},
	}
	err = c.Watch(&source.Kind{Type: &v1.Secret{}}, &handler.EnqueueRequestForObject{}, namePredicate)
	if err != nil {
		return err
	}
	return nil
}

// Reconcile reads that state of the cluster for a ClusterRegistration object and makes changes based on the state read
// and what is in the ClusterRegistration.Spec
func (r *ReconcileClusterRegistration) Reconcile(request reconcile.Request) (reconcile.Result, error) {
	reqLogger := log.WithValues("Request.Namespace", request.Namespace, "Request.Name", request.Name)
	reqLogger.Info("Reconciling ClusterRegistration")

	//Fetch the Secret with name redhat-marketplace-pull-secret
	rhmPullSecret := v1.Secret{}
	err := r.client.Get(context.TODO(),
		types.NamespacedName{Name: request.Name, Namespace: request.Namespace},
		&rhmPullSecret)
	if err != nil {
		if k8serrors.IsNotFound(err) {
			reqLogger.Error(err, "can't find the secret")
			return reconcile.Result{}, err
		}

		reqLogger.Error(err, "fetch failed")

		return reconcile.Result{}, err
	}

	annotations := rhmPullSecret.GetAnnotations()
	if annotations == nil {
		annotations = make(map[string]string)
	}
	reqLogger.Info("redhat-marketplace-pull-secret Secret found")

	// Check condition if 'PULL_SECRET' key is missing in secret
	if _, ok := rhmPullSecret.Data[utils.RHMPullSecretKey]; !ok {
		reqLogger.Info("Missing token filed in secret")
		annotations[utils.RHMPullSecretStatus] = "error"
		annotations[utils.RHMPullSecretMessage] = "key with name 'PULL_SECRET' is missing in secret"
		rhmPullSecret.SetAnnotations(annotations)
		if err := r.client.Update(context.TODO(), &rhmPullSecret); err != nil {
			reqLogger.Error(err, "Failed to patch secret with Endpoint status")
		}
		reqLogger.Info("Secret updated with status on failiure")
		return reconcile.Result{}, err
	}

	//Get Account Id from Pull Secret Token
	rhmAccountId, err := marketplace.GetAccountIdFromJWTToken(string(rhmPullSecret.Data[utils.RHMPullSecretKey]))
	if rhmAccountId == "" || err != nil {
		reqLogger.Error(err, "Token is missing account id")
		annotations[utils.RHMPullSecretStatus] = "error"
		annotations[utils.RHMPullSecretMessage] = "Account id is not available in provided token, please generate token from RH Marketplace again"
		rhmPullSecret.SetAnnotations(annotations)
		if err := r.client.Update(context.TODO(), &rhmPullSecret); err != nil {
			reqLogger.Error(err, "Failed to patch secret with Endpoint status")
		}
		reqLogger.Info("Secret updated with account id missing error")
		return reconcile.Result{}, err
	}

	reqLogger.Info("account id found in token")
	pullSecret, ok := rhmPullSecret.Data[utils.RHMPullSecretKey]

	if !ok {
		err := errors.New("rhm pull secret not found")
		reqLogger.Error(err, "couldn't find pull secret")
		return reconcile.Result{}, err
	}

	cfg, _ := config.GetConfig()

	mclient, err := marketplace.NewMarketplaceClient(&marketplace.MarketplaceClientConfig{
		Url:      cfg.Marketplace.URL, // parameterize this for dev
		Token:    string(pullSecret),
		Insecure: cfg.Marketplace.InsecureClient,
	})

	if err != nil {
		reqLogger.Error(err, "failed to build marketplaceclient")
		return reconcile.Result{}, nil
	}


	newMarketplaceConfig := &marketplacev1alpha1.MarketplaceConfig{}
	err = r.client.Get(context.TODO(), types.NamespacedName{
		Namespace: request.Namespace,
		Name:      "marketplaceconfig",
	}, newMarketplaceConfig)

	if err != nil && k8serrors.IsNotFound(err) {
		newMarketplaceConfig = nil
	}

	if newMarketplaceConfig != nil {
		reqLogger.Info("MarketPlace config object found, check status if its installed or not")
		//Setting MarketplaceClientAccount

		marketplaceClientAccount := &marketplace.MarketplaceClientAccount{
			AccountId:   newMarketplaceConfig.Spec.RhmAccountID,
			ClusterUuid: newMarketplaceConfig.Spec.ClusterUUID,
		}

		// Marketplace config object found
		reqLogger.Info("Pulling MarketPlace config object status")
		registrationStatusOutput, _ := mclient.RegistrationStatus(marketplaceClientAccount)

		if registrationStatusOutput.RegistrationStatus == marketplace.RegistrationStatusInstalled {
			reqLogger.Info("MarketPlace config object is already registered for account")

			//Update secret with status
			if annotations[utils.RHMPullSecretStatus] != "success" {
				reqLogger.Info("Updating secret with success status")
				annotations[utils.RHMPullSecretStatus] = "success"
				annotations[utils.RHMPullSecretMessage] = "rhm-operator-secret generated successfully"
				rhmPullSecret.SetAnnotations(annotations)
				if err := r.client.Update(context.TODO(), &rhmPullSecret); err != nil {
					reqLogger.Error(err, "Failed to patch secret with Endpoint status")
					return reconcile.Result{}, err
				}
				reqLogger.Info("Secret updated with status on success")
			}
		}
	}

	reqLogger.Info("RHMarketPlace Pull Secret token found")
	//Calling POST endpoint to pull the secret definition
	newOptSecretObj, err := mclient.GetMarketplaceSecret()
	if err != nil {
		reqLogger.Info("RHMarketPlaceSecret failure")
		reqLogger.Error(err, "RHMarketPlaceSecret failure")
		annotations[utils.RHMPullSecretStatus] = "error"
		annotations[utils.RHMPullSecretMessage] = err.Error()
		rhmPullSecret.SetAnnotations(annotations)
		if err := r.client.Update(context.TODO(), &rhmPullSecret); err != nil {
			reqLogger.Error(err, "Failed to patch secret with Endpoint status")
		}
		return reconcile.Result{}, err
	}
	newOptSecretObj.SetNamespace(request.Namespace)

	//Fetch the Secret with name redhat-Operator-secret
	secretKeyname := types.NamespacedName{
		Name:      newOptSecretObj.Name,
		Namespace: newOptSecretObj.Namespace,
	}

	reqLogger.Info("retrieving secret", "name", secretKeyname.Name, "namespace", secretKeyname.Namespace)

	optSecret := &v1.Secret{}
	err = r.client.Get(context.TODO(), secretKeyname, optSecret)
	if err != nil {
		if !k8serrors.IsNotFound(err) {
			reqLogger.Error(err, "bad error getting secret")
			return reconcile.Result{}, err
		}

		reqLogger.Info("secret not found, creating")
		err = r.client.Create(context.TODO(), newOptSecretObj)
		if err != nil {
			reqLogger.Error(err, "Failed to Create Secret Object")
			return reconcile.Result{}, err
		}
	} else {
		reqLogger.Info("Comparing old and new rhm-operator-secret")

		if !reflect.DeepEqual(newOptSecretObj.Data, optSecret.Data) {
			reqLogger.Info("rhm-operator-secret are different copy")
			optSecret.Data = newOptSecretObj.Data

			err := r.client.Update(context.TODO(), optSecret)
			if err != nil {
				reqLogger.Error(err, "could not update rhm-operator-secret with new object", "Resource", utils.RHMOperatorSecretName)
				return reconcile.Result{}, err
			}
		}
	}

	//Update secret with status
	if annotations[utils.RHMPullSecretStatus] != "success" {
		reqLogger.Info("Updating secret with success status")
		annotations[utils.RHMPullSecretStatus] = "success"
		annotations[utils.RHMPullSecretMessage] = "rhm-operator-secret generated successfully"
		rhmPullSecret.SetAnnotations(annotations)
		if err := r.client.Update(context.TODO(), &rhmPullSecret); err != nil {
			reqLogger.Error(err, "Failed to patch secret with Endpoint status")
			return reconcile.Result{}, err
		}
		reqLogger.Info("Secret updated with status on success")
	}


	//Create Markeplace Config object
	reqLogger.Info("finding clusterversion resource")
	clusterVersion := &openshiftconfigv1.ClusterVersion{}
	err = r.client.Get(context.Background(), client.ObjectKey{
		Name: "version",
	}, clusterVersion)

	if err != nil {
		if !k8serrors.IsNotFound(err) && !meta.IsNoMatchError(err) {
			reqLogger.Error(err, "Failed to retrieve clusterversion resource")
			return reconcile.Result{}, err
		}
		clusterVersion = nil
	}

	var clusterID string
	if clusterVersion != nil {
		clusterID = string(clusterVersion.Spec.ClusterID)
		reqLogger.Info("Clusterversion object found with clusterID", "clusterID", clusterID)
	} else {
		clusterID = uuid.New().String()
		reqLogger.Info("Clusterversion object not found, generating clusterID", "clusterID", clusterID)
	}

	newMarketplaceConfig = &marketplacev1alpha1.MarketplaceConfig{}
	err = r.client.Get(context.TODO(), types.NamespacedName{
		Namespace: request.Namespace,
		Name:      "marketplaceconfig",
	}, newMarketplaceConfig)

	if err != nil {
		if k8serrors.IsNotFound(err) {
			newMarketplaceConfig.ObjectMeta.Name = "marketplaceconfig"
			newMarketplaceConfig.ObjectMeta.Namespace = request.Namespace
			newMarketplaceConfig.Spec.ClusterUUID = string(clusterID)
			newMarketplaceConfig.Spec.RhmAccountID = rhmAccountId
			// Create Marketplace Config object with ClusterID
			reqLogger.Info("Marketplace Config creating")
			err = r.client.Create(context.TODO(), newMarketplaceConfig)
			if err != nil {
				reqLogger.Error(err, "Failed to Create Marketplace Config Object")
				return reconcile.Result{}, err
			}

			return reconcile.Result{Requeue: true}, nil
		}

		reqLogger.Error(err, "failed to get marketplaceconfig")
		return reconcile.Result{}, err
	}

	owners := newMarketplaceConfig.GetOwnerReferences()

	if newMarketplaceConfig.Spec.ClusterUUID != string(clusterID) ||
		newMarketplaceConfig.Spec.RhmAccountID != rhmAccountId ||
		!reflect.DeepEqual(newMarketplaceConfig.GetOwnerReferences(), owners) {

		newMarketplaceConfig.Spec.ClusterUUID = string(clusterID)
		newMarketplaceConfig.Spec.RhmAccountID = rhmAccountId

		err = r.client.Update(context.TODO(), newMarketplaceConfig)
		if err != nil {
			reqLogger.Error(err, "Failed to update Marketplace Config Object")
			return reconcile.Result{}, err
		}
	}

	ownerFound := false
	for _, owner := range rhmPullSecret.ObjectMeta.OwnerReferences {
		if owner.Name == rhmPullSecret.Name &&
			owner.Kind == rhmPullSecret.Kind &&
			owner.APIVersion == rhmPullSecret.APIVersion {
			ownerFound = true
		}
	}

	if err := controllerutil.SetOwnerReference(
		newMarketplaceConfig,
		&rhmPullSecret,
		r.scheme); !ownerFound && err == nil {
		r.client.Update(context.TODO(), &rhmPullSecret)
		if err != nil {
			reqLogger.Error(err, "Failed to update Marketplace Config Object")
			return reconcile.Result{}, err
		}
	}

	reqLogger.Info("reconcile finished. Marketplace Config Created")
	return reconcile.Result{}, nil
}
