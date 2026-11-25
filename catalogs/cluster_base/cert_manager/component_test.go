/*
SPDX-FileCopyrightText: Copyright (c) 2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
SPDX-License-Identifier: Apache-2.0
*/

package certmanager

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	admissionregistrationv1 "k8s.io/api/admissionregistration/v1"
	coordinationv1 "k8s.io/api/coordination/v1"
	corev1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	cclusternvidiacomv1alpha1 "github.com/NVIDIA/ccluster/api/ccluster/v1alpha1"
)

// webhookAt returns the objects of a served cert-manager webhook as the upstream chart
// renders it for release, in namespace, with webhook as its entry name.
func webhookAt(release, namespace, webhook string) []client.Object {
	svc := release + "-webhook"
	return []client.Object{
		&admissionregistrationv1.ValidatingWebhookConfiguration{
			ObjectMeta: metav1.ObjectMeta{Name: release + "-webhook"},
			Webhooks: []admissionregistrationv1.ValidatingWebhook{{
				Name: webhook,
				ClientConfig: admissionregistrationv1.WebhookClientConfig{
					Service:  &admissionregistrationv1.ServiceReference{Namespace: namespace, Name: svc},
					CABundle: []byte("ca"),
				},
			}},
		},
		&corev1.Service{ObjectMeta: metav1.ObjectMeta{Namespace: namespace, Name: svc}},
		&discoveryv1.EndpointSlice{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: namespace,
				Name:      svc + "-abcde",
				Labels:    map[string]string{discoveryv1.LabelServiceName: svc},
			},
			Endpoints: []discoveryv1.Endpoint{{
				Addresses:  []string{"10.0.0.1"},
				Conditions: discoveryv1.EndpointConditions{Ready: new(true)},
			}},
		},
	}
}

// crdsExcept returns the CustomResourceDefinitions the catalog's cert-manager kinds need,
// leaving out those named in missing.
func crdsExcept(missing ...string) []client.Object {
	var objs []client.Object
	for _, name := range []string{
		"certificates.cert-manager.io", "clusterissuers.cert-manager.io", "issuers.cert-manager.io",
	} {
		if !slices.Contains(missing, name) {
			objs = append(objs, &apiextensionsv1.CustomResourceDefinition{ObjectMeta: metav1.ObjectMeta{Name: name}})
		}
	}
	return objs
}

// lease returns the controller's Lease in namespace, held by holder ("" for released),
// with the upstream 60-second duration, renewed age ago.
func lease(namespace, holder string, age time.Duration) *coordinationv1.Lease {
	return &coordinationv1.Lease{
		ObjectMeta: metav1.ObjectMeta{Namespace: namespace, Name: "cert-manager-controller"},
		Spec: coordinationv1.LeaseSpec{
			HolderIdentity:       new(holder),
			LeaseDurationSeconds: new(int32(60)),
			RenewTime:            &metav1.MicroTime{Time: time.Now().Add(-age)},
		},
	}
}

// TestVerify pins the check to cert-manager's webhook entry name, CRD names and Lease
// name and nothing else: the release name and namespace a cluster installed it under do
// not matter.
func TestVerify(t *testing.T) {
	const holder = "cert-manager-abc-external-cert-manager-controller"
	installed := func(objs ...client.Object) []client.Object {
		return append(append(webhookAt("cluster-base-cert-manager", "cert-manager", "webhook.cert-manager.io"),
			crdsExcept()...), objs...)
	}
	tests := []struct {
		name    string
		objects []client.Object
		want    string // "" means "expect nil"; otherwise a prefix of the error
	}{
		{
			name:    "as cluster_base installs it",
			objects: installed(lease("kube-system", holder, time.Second)),
		},
		{
			// The chart's global.leaderElection.namespace moved the Lease too.
			name: "another release and namespace",
			objects: append(append(webhookAt("cm", "security", "webhook.cert-manager.io"), crdsExcept()...),
				lease("security", holder, time.Second)),
		},
		{
			name: "not installed",
			want: "no ValidatingWebhookConfiguration has webhook webhook.cert-manager.io",
		},
		{
			// ingress-nginx's webhook, also present on a cluster_base cluster.
			name:    "another component's webhook",
			objects: webhookAt("ingress-nginx", "ingress-nginx", "validate.nginx.ingress.kubernetes.io"),
			want:    "no ValidatingWebhookConfiguration has webhook webhook.cert-manager.io",
		},
		{
			// The upstream chart with installCRDs left off.
			name:    "webhook served without the CRDs",
			objects: webhookAt("cm", "security", "webhook.cert-manager.io"),
			want:    "CustomResourceDefinition certificates.cert-manager.io not found",
		},
		{
			name: "one CRD missing",
			objects: append(webhookAt("cm", "security", "webhook.cert-manager.io"),
				crdsExcept("issuers.cert-manager.io")...),
			want: "CustomResourceDefinition issuers.cert-manager.io not found",
		},
		{
			// Never started, or started with leader election off.
			name:    "no controller",
			objects: installed(),
			want:    "no Lease cert-manager-controller found in any namespace",
		},
		{
			// A controller that stops cleanly releases its Lease.
			name:    "controller stopped",
			objects: installed(lease("kube-system", "", time.Second)),
			want: "no controller holds Lease cert-manager-controller: " +
				"Lease kube-system/cert-manager-controller has no holder",
		},
		{
			// A controller that crashes leaves its Lease to go stale.
			name:    "controller crashed",
			objects: installed(lease("kube-system", holder, time.Hour)),
			want: "no controller holds Lease cert-manager-controller: " +
				"Lease kube-system/cert-manager-controller last renewed at ",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			scheme := runtime.NewScheme()
			for _, add := range []func(*runtime.Scheme) error{
				admissionregistrationv1.AddToScheme, apiextensionsv1.AddToScheme,
				coordinationv1.AddToScheme, corev1.AddToScheme, discoveryv1.AddToScheme,
			} {
				if err := add(scheme); err != nil {
					t.Fatal(err)
				}
			}
			// The index stands in for the API server's metadata.name field selector.
			r := fake.NewClientBuilder().WithScheme(scheme).WithObjects(tt.objects...).
				WithIndex(&coordinationv1.Lease{}, "metadata.name", func(o client.Object) []string {
					return []string{o.GetName()}
				}).Build()
			err := Verify(context.Background(), r, cclusternvidiacomv1alpha1.CCLClusterConfigSpec{})
			switch {
			case tt.want == "" && err != nil:
				t.Fatalf("want nil, got %v", err)
			case tt.want != "" && (err == nil || !strings.HasPrefix(err.Error(), tt.want)):
				t.Fatalf("want prefix %q, got %v", tt.want, err)
			}
		})
	}
}
