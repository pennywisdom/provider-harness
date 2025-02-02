/*
Copyright 2022 The Crossplane Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package agent

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/harness/harness-go-sdk/harness/nextgen"
	"github.com/hashicorp/go-retryablehttp"
	"github.com/pkg/errors"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	xpv1 "github.com/crossplane/crossplane-runtime/apis/common/v1"
	"github.com/crossplane/crossplane-runtime/pkg/connection"
	"github.com/crossplane/crossplane-runtime/pkg/controller"
	"github.com/crossplane/crossplane-runtime/pkg/event"
	"github.com/crossplane/crossplane-runtime/pkg/ratelimiter"
	"github.com/crossplane/crossplane-runtime/pkg/reconciler/managed"
	"github.com/crossplane/crossplane-runtime/pkg/resource"

	"github.com/crossplane/provider-harness/apis/gitops/v1alpha1"
	apisv1alpha1 "github.com/crossplane/provider-harness/apis/v1alpha1"
	"github.com/crossplane/provider-harness/internal/features"
)

const (
	errNotAgent     = "managed resource is not a Agent custom resource"
	errTrackPCUsage = "cannot track ProviderConfig usage"
	errGetPC        = "cannot get ProviderConfig"
	errGetCreds     = "cannot get credentials"

	errNewClient = "cannot create new Service"
)

// A HarnessService does nothing.
type HarnessService struct {
	*nextgen.APIClient
}

var newHarnessService = func(creds []byte) (*HarnessService, error) {
	config := nextgen.NewConfiguration()
	config.BasePath = "https://app.harness.io"

	config.HTTPClient = &retryablehttp.Client{
		RetryMax:     10,
		RetryWaitMin: 5 * time.Second,
		RetryWaitMax: 10 * time.Second,
		HTTPClient: &http.Client{
			Timeout: 10 * time.Second,
		},
		Backoff:    retryablehttp.DefaultBackoff,
		CheckRetry: retryablehttp.DefaultRetryPolicy,
	}

	client := nextgen.NewAPIClient(config)

	return &HarnessService{
		client,
	}, nil
}

// Setup adds a controller that reconciles Agent managed resources.
func Setup(mgr ctrl.Manager, o controller.Options) error {
	name := managed.ControllerName(v1alpha1.AgentGroupKind)

	cps := []managed.ConnectionPublisher{managed.NewAPISecretPublisher(mgr.GetClient(), mgr.GetScheme())}
	if o.Features.Enabled(features.EnableAlphaExternalSecretStores) {
		cps = append(cps, connection.NewDetailsManager(mgr.GetClient(), apisv1alpha1.StoreConfigGroupVersionKind))
	}

	r := managed.NewReconciler(mgr,
		resource.ManagedKind(v1alpha1.AgentGroupVersionKind),
		managed.WithExternalConnecter(&connector{
			kube:         mgr.GetClient(),
			usage:        resource.NewProviderConfigUsageTracker(mgr.GetClient(), &apisv1alpha1.ProviderConfigUsage{}),
			newServiceFn: newHarnessService,
		}),
		managed.WithLogger(o.Logger.WithValues("controller", name)),
		managed.WithPollInterval(o.PollInterval),
		managed.WithRecorder(event.NewAPIRecorder(mgr.GetEventRecorderFor(name))),
		managed.WithConnectionPublishers(cps...))

	return ctrl.NewControllerManagedBy(mgr).
		Named(name).
		WithOptions(o.ForControllerRuntime()).
		WithEventFilter(resource.DesiredStateChanged()).
		For(&v1alpha1.Agent{}).
		Complete(ratelimiter.NewReconciler(name, r, o.GlobalRateLimiter))
}

// A connector is expected to produce an ExternalClient when its Connect method
// is called.
type connector struct {
	kube         client.Client
	usage        resource.Tracker
	newServiceFn func(creds []byte) (*HarnessService, error)
}

// Connect typically produces an ExternalClient by:
// 1. Tracking that the managed resource is using a ProviderConfig.
// 2. Getting the managed resource's ProviderConfig.
// 3. Getting the credentials specified by the ProviderConfig.
// 4. Using the credentials to form a client.
func (c *connector) Connect(ctx context.Context, mg resource.Managed) (managed.ExternalClient, error) {
	cr, ok := mg.(*v1alpha1.Agent)
	if !ok {
		return nil, errors.New(errNotAgent)
	}

	if err := c.usage.Track(ctx, mg); err != nil {
		return nil, errors.Wrap(err, errTrackPCUsage)
	}

	pc := &apisv1alpha1.ProviderConfig{}
	if err := c.kube.Get(ctx, types.NamespacedName{Name: cr.GetProviderConfigReference().Name}, pc); err != nil {
		return nil, errors.Wrap(err, errGetPC)
	}

	cd := pc.Spec.Credentials
	data, err := resource.CommonCredentialExtractor(ctx, cd.Source, c.kube, cd.CommonCredentialSelectors)
	if err != nil {
		return nil, errors.Wrap(err, errGetCreds)
	}

	svc, err := c.newServiceFn(data)
	if err != nil {
		return nil, errors.Wrap(err, errNewClient)
	}

	return &external{service: svc}, nil
}

// An ExternalClient observes, then either creates, updates, or deletes an
// external resource to ensure it reflects the managed resource's desired state.
type external struct {
	// A 'client' used to connect to the external resource API. In practice this
	// would be something like an AWS SDK client.
	service *HarnessService
}

func (c *external) Observe(ctx context.Context, mg resource.Managed) (managed.ExternalObservation, error) {
	cr, ok := mg.(*v1alpha1.Agent)
	if !ok {
		return managed.ExternalObservation{}, errors.New(errNotAgent)
	}

	identifier := ""
	if cr.Spec.ForProvider.Identifier != nil {
		identifier = *cr.Spec.ForProvider.Identifier
	}

	if cr.Spec.ForProvider.AccountIdentifier == nil {
		log.Fatalln("AccountIndentifier is required")
	}

	ctx = context.WithValue(ctx, nextgen.ContextAPIKey, nextgen.APIKey{Key: os.Getenv("HARNESS_API_KEY")})

	agent, response, err := c.service.AgentApi.AgentServiceForServerGet(
		ctx,
		identifier,
		*cr.Spec.ForProvider.AccountIdentifier, nil)
	defer func() {
		if response != nil {
			err = response.Body.Close()
			if err != nil {
				log.Fatal(err)
			}
		}
	}()

	// log.Printf("%v\n", err)
	// log.Printf("%+v", response)

	// if response == nil || response.StatusCode == http.StatusNotFound {
	if err != nil || (response != nil && response.StatusCode == http.StatusNotFound) {
		//nolint:nilerr
		return managed.ExternalObservation{
			ResourceExists: false,
		}, nil
	}

	if *agent.Health.HarnessGitopsAgent.Status == nextgen.HEALTHY_Servicev1HealthStatus {
		cr.Status.SetConditions(xpv1.Available())
	}

	return managed.ExternalObservation{
		// Return false when the external resource does not exist. This lets
		// the managed resource reconciler know that it needs to call Create to
		// (re)create the resource, or that it has successfully been deleted.
		ResourceExists: true,

		// Return false when the external resource exists, but it not up to date
		// with the desired managed resource state. This lets the managed
		// resource reconciler know that it needs to call Update.
		ResourceUpToDate: true,

		// Return any details that may be required to connect to the external
		// resource. These will be stored as the connection secret.
		// ConnectionDetails: managed.ConnectionDetails{},
	}, nil
}

func (c *external) Create(ctx context.Context, mg resource.Managed) (managed.ExternalCreation, error) {
	cr, ok := mg.(*v1alpha1.Agent)
	if !ok {
		return managed.ExternalCreation{}, errors.New(errNotAgent)
	}

	accountIdentifier := ""
	if cr.Spec.ForProvider.AccountIdentifier != nil {
		accountIdentifier = *cr.Spec.ForProvider.AccountIdentifier
		log.Printf("%s\n", accountIdentifier)
	}

	projectIndentifier := ""
	if cr.Spec.ForProvider.ProjectIdentifier != nil {
		projectIndentifier = *cr.Spec.ForProvider.ProjectIdentifier
		log.Printf("%s\n", projectIndentifier)
	}

	orgIdentifier := ""
	if cr.Spec.ForProvider.OrgIdentifier != nil {
		orgIdentifier = *cr.Spec.ForProvider.OrgIdentifier
		log.Printf("%s\n", orgIdentifier)
	}

	description := ""
	if cr.Spec.ForProvider.Description != nil {
		description = *cr.Spec.ForProvider.Description
		log.Printf("%s\n", description)
	}

	name := cr.GetObjectMeta().GetName()
	ctx = context.WithValue(ctx, nextgen.ContextAPIKey, nextgen.APIKey{Key: os.Getenv("HARNESS_API_KEY")})
	agent, response, err := c.service.AgentApi.AgentServiceForServerCreate(
		ctx,
		nextgen.V1Agent{
			AccountIdentifier: accountIdentifier,
			ProjectIdentifier: projectIndentifier,
			OrgIdentifier:     orgIdentifier,
			Identifier:        "",
			Name:              name,
			Metadata: &nextgen.V1AgentMetadata{
				Namespace:        "harness",
				HighAvailability: true,
				// DeployedApplicationCount: 0,
				// ExistingInstallation:     false,
				MappedProjects: &nextgen.Servicev1AppProjectMapping{},
			},
			Description: description,
			// Type_:       &nextgen.MANAGED_ARGO_PROVIDER_V1AgentType,
			// CreatedAt:         &nextgen.V1Time{
			// 	Seconds: "",
			// 	Nanos:   0,
			// },
			// LastModifiedAt:    &nextgen.V1Time{},
			// Tags: map[string]string{},
			// Health:            &nextgen.V1AgentHealth{},
			// Credentials:       &nextgen.V1AgentCredentials{},
			// Version:           &nextgen.V1SemanticVersion{},
			// UpgradeAvailable:  false,
			// Scope:             &"",
		})
	defer func() {
		if response != nil {
			err := response.Body.Close()
			if err != nil {
				log.Fatal(err)
			}
		}
	}()

	if err != nil {
		return managed.ExternalCreation{}, err
	}
	// if response.StatusCode != http.StatusCreated {
	// 	return managed.ExternalCreation{}, errors.Errorf("Agent could not be created status: %s, status code %d", response.Status, response.StatusCode)
	// }

	cr.Status.AtProvider.State = string(*agent.Health.HarnessGitopsAgent.Status)

	return managed.ExternalCreation{
		// Optionally return any details that may be required to connect to the
		// external resource. These will be stored as the connection secret.
		ConnectionDetails: managed.ConnectionDetails{},
	}, nil
}

func (c *external) Update(ctx context.Context, mg resource.Managed) (managed.ExternalUpdate, error) {
	// No update required?
	return managed.ExternalUpdate{}, nil

	// cr, ok := mg.(*v1alpha1.Agent)
	// if !ok {
	// 	return managed.ExternalUpdate{}, errors.New(errNotAgent)
	// }

	// fmt.Printf("Updating: %+v", cr)

	// return managed.ExternalUpdate{
	// 	// Optionally return any details that may be required to connect to the
	// 	// external resource. These will be stored as the connection secret.
	// 	ConnectionDetails: managed.ConnectionDetails{},
	// }, nil
}

func (c *external) Delete(ctx context.Context, mg resource.Managed) error {
	cr, ok := mg.(*v1alpha1.Agent)
	if !ok {
		return errors.New(errNotAgent)
	}

	fmt.Printf("Deleting: %+v", cr)

	return nil
}
