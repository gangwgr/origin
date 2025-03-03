/*
Copyright 2023 The Kubernetes Authors.

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

package apimachinery

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/onsi/ginkgo/v2"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apiextensionclientset "k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset"
	"k8s.io/apiextensions-apiserver/test/integration/fixtures"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/openapi3"
	aggregatorclient "k8s.io/kube-aggregator/pkg/client/clientset_generated/clientset"
	"k8s.io/kube-openapi/pkg/spec3"
	imageutils "k8s.io/kubernetes/test/utils/image"
	admissionapi "k8s.io/pod-security-admission/api"

	"k8s.io/kubernetes/test/e2e/framework"

	// ensure libs have a chance to initialize
	_ "github.com/stretchr/testify/assert"
)

var _ = SIGDescribe("OpenAPIV3", func() {
	f := framework.NewDefaultFramework("openapiv3")
	f.NamespacePodSecurityLevel = admissionapi.LevelBaseline

	/*
		Release : v1.27
		Testname: OpenAPI V3 RoundTrip
		Description: Fetch the OpenAPI v3 of all built-in group versions. The OpenAPI specs MUST roundtrip successfully.
	*/
	ginkgo.It("should round trip OpenAPI V3 for all built-in group versions", func(ctx context.Context) {
		c := openapi3.NewRoot(f.ClientSet.Discovery().OpenAPIV3())
		gvs, err := c.GroupVersions()
		framework.ExpectNoError(err)
		// List of built in types that do not contain the k8s.io suffix
		builtinGVs := map[string]bool{
			"apps":        true,
			"autoscaling": true,
			"batch":       true,
			"policy":      true,
		}

		for _, gv := range gvs {
			// Prevent race conditions with looking up gvs of CRDs and
			// other aggregated apiservers added by other tests
			if !strings.HasSuffix(gv.Group, "k8s.io") && !builtinGVs[gv.Group] {
				continue
			}
			spec1, err := c.GVSpec(gv)
			framework.ExpectNoError(err)
			specMarshalled, err := json.Marshal(spec1)
			framework.ExpectNoError(err)
			var spec2 spec3.OpenAPI
			json.Unmarshal(specMarshalled, &spec2)

			if !reflect.DeepEqual(*spec1, spec2) {
				diff := cmp.Diff(*spec1, spec2)
				framework.Failf("%s", diff)
			}
		}
	})

	/*
		Release : v1.27
		Testname: OpenAPI V3 CustomResourceDefinition
		Description: Create a CustomResourceDefinition. The OpenAPI V3 document of the CustomResourceDefinition MUST be created. The OpenAPI V3 MUST be round trippable.
	*/
	ginkgo.It("should publish OpenAPI V3 for CustomResourceDefinition", func(ctx context.Context) {
		config, err := framework.LoadConfig()
		framework.ExpectNoError(err)
		apiExtensionClient, err := apiextensionclientset.NewForConfig(config)
		framework.ExpectNoError(err)
		dynamicClient, err := dynamic.NewForConfig(config)
		framework.ExpectNoError(err)
		crd := fixtures.NewRandomNameV1CustomResourceDefinition(apiextensionsv1.NamespaceScoped)
		gv := schema.GroupVersion{Group: crd.Spec.Group, Version: crd.Spec.Versions[0].Name}
		_, err = fixtures.CreateNewV1CustomResourceDefinition(crd, apiExtensionClient, dynamicClient)
		defer func() {
			err = fixtures.DeleteV1CustomResourceDefinition(crd, apiExtensionClient)
			framework.ExpectNoError(err, "deleting CustomResourceDefinition")
		}()

		framework.ExpectNoError(err)
		c := openapi3.NewRoot(f.ClientSet.Discovery().OpenAPIV3())
		var openAPISpec *spec3.OpenAPI
		// Poll for the OpenAPI to be updated with the new CRD
		wait.Poll(time.Second*1, wait.ForeverTestTimeout, func() (bool, error) {
			openAPISpec, err = c.GVSpec(gv)
			if err == nil {
				return true, nil
			}
			return false, nil
		})

		specMarshalled, err := json.Marshal(openAPISpec)
		framework.ExpectNoError(err)
		var spec2 spec3.OpenAPI
		json.Unmarshal(specMarshalled, &spec2)

		if !reflect.DeepEqual(*openAPISpec, spec2) {
			diff := cmp.Diff(*openAPISpec, spec2)
			framework.Failf("%s", diff)
		}
	})

	/*
		Release : v1.27
		Testname: OpenAPI V3 Aggregated APIServer
		Description: Create an Aggregated APIServer. The OpenAPI V3 for the aggregated apiserver MUST be aggregated by the aggregator and published. The specification MUST be round trippable.
	*/
	ginkgo.It("should contain OpenAPI V3 for Aggregated APIServer", func(ctx context.Context) {
		config, err := framework.LoadConfig()
		framework.ExpectNoError(err)
		aggrclient, err := aggregatorclient.NewForConfig(config)
		framework.ExpectNoError(err)
		names := generateSampleAPIServerObjectNames(f.Namespace.Name)
		SetUpSampleAPIServer(ctx, f, aggrclient, imageutils.GetE2EImage(imageutils.APIServer), names)
		defer cleanupSampleAPIServer(ctx, f.ClientSet, aggrclient, names)

		c := openapi3.NewRoot(f.ClientSet.Discovery().OpenAPIV3())
		gv := schema.GroupVersion{Group: "wardle.example.com", Version: "v1alpha1"}
		var openAPISpec *spec3.OpenAPI
		// Poll for the OpenAPI to be updated with the new aggregated apiserver.
		wait.Poll(time.Second*1, wait.ForeverTestTimeout, func() (bool, error) {
			openAPISpec, err = c.GVSpec(gv)
			if err == nil {
				return true, nil
			}
			return false, nil
		})

		specMarshalled, err := json.Marshal(openAPISpec)
		framework.ExpectNoError(err)
		var spec2 spec3.OpenAPI
		json.Unmarshal(specMarshalled, &spec2)

		if !reflect.DeepEqual(*openAPISpec, spec2) {
			diff := cmp.Diff(*openAPISpec, spec2)
			framework.Failf("%s", diff)
		}
	})
})
