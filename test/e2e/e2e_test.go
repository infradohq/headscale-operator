//go:build e2e
// +build e2e

/*
Copyright 2025.

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

package e2e

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/infradohq/headscale-operator/test/utils"
)

// namespace where the project is deployed in
const namespace = "headscale-operator-system"

// testNamespace is the namespace for test resources
const testNamespace = "headscale-e2e-test"

// serviceAccountName created for the project
const serviceAccountName = "headscale-operator-controller-manager"

// metricsServiceName is the name of the metrics service of the project
const metricsServiceName = "headscale-operator-controller-manager-metrics-service"

// metricsRoleBindingName is the name of the RBAC that will be created to allow get the metrics data
const metricsRoleBindingName = "headscale-operator-metrics-binding"

var _ = Describe("Manager", Ordered, func() {
	var controllerPodName string

	// Before running the tests, set up the environment by creating the namespace,
	// enforce the restricted security policy to the namespace, installing CRDs,
	// and deploying the controller.
	BeforeAll(func() {
		By("creating manager namespace")
		cmd := exec.Command("kubectl", "create", "ns", namespace)
		_, err := utils.Run(cmd)
		Expect(err).NotTo(HaveOccurred(), "Failed to create namespace")

		By("labeling the namespace to enforce the restricted security policy")
		cmd = exec.Command("kubectl", "label", "--overwrite", "ns", namespace,
			"pod-security.kubernetes.io/enforce=restricted")
		_, err = utils.Run(cmd)
		Expect(err).NotTo(HaveOccurred(), "Failed to label namespace with restricted policy")

		By("installing CRDs")
		cmd = exec.Command("make", "install")
		_, err = utils.Run(cmd)
		Expect(err).NotTo(HaveOccurred(), "Failed to install CRDs")

		By("deploying the controller-manager")
		cmd = exec.Command("make", "deploy", fmt.Sprintf("IMG=%s", managerImage))
		_, err = utils.Run(cmd)
		Expect(err).NotTo(HaveOccurred(), "Failed to deploy the controller-manager")

		By("creating test namespace for e2e resources")
		cmd = exec.Command("kubectl", "create", "ns", testNamespace)
		_, _ = utils.Run(cmd) // Ignore error if namespace already exists
	})

	// After all tests have been executed, clean up by undeploying the controller, uninstalling CRDs,
	// and deleting the namespace.
	AfterAll(func() {
		By("cleaning up the curl pod for metrics")
		cmd := exec.Command("kubectl", "delete", "pod", "curl-metrics", "-n", namespace, "--ignore-not-found=true")
		_, _ = utils.Run(cmd)

		By("cleaning up test namespace")
		cmd = exec.Command("kubectl", "delete", "ns", testNamespace, "--ignore-not-found=true")
		_, _ = utils.Run(cmd)

		By("undeploying the controller-manager")
		cmd = exec.Command("make", "undeploy")
		_, _ = utils.Run(cmd)

		By("uninstalling CRDs")
		cmd = exec.Command("make", "uninstall")
		_, _ = utils.Run(cmd)

		By("removing manager namespace")
		cmd = exec.Command("kubectl", "delete", "ns", namespace)
		_, _ = utils.Run(cmd)
	})

	// After each test, check for failures and collect logs, events,
	// and pod descriptions for debugging.
	AfterEach(func() {
		specReport := CurrentSpecReport()
		if specReport.Failed() {
			By("Fetching controller manager pod logs")
			cmd := exec.Command("kubectl", "logs", controllerPodName, "-n", namespace)
			controllerLogs, err := utils.Run(cmd)
			if err == nil {
				_, _ = fmt.Fprintf(GinkgoWriter, "Controller logs:\n %s", controllerLogs)
			} else {
				_, _ = fmt.Fprintf(GinkgoWriter, "Failed to get Controller logs: %s", err)
			}

			By("Fetching Kubernetes events")
			cmd = exec.Command("kubectl", "get", "events", "-n", testNamespace, "--sort-by=.lastTimestamp")
			eventsOutput, err := utils.Run(cmd)
			if err == nil {
				_, _ = fmt.Fprintf(GinkgoWriter, "Kubernetes events:\n%s", eventsOutput)
			} else {
				_, _ = fmt.Fprintf(GinkgoWriter, "Failed to get Kubernetes events: %s", err)
			}

			By("Fetching curl-metrics logs")
			cmd = exec.Command("kubectl", "logs", "curl-metrics", "-n", namespace)
			metricsOutput, err := utils.Run(cmd)
			if err == nil {
				_, _ = fmt.Fprintf(GinkgoWriter, "Metrics logs:\n %s", metricsOutput)
			} else {
				_, _ = fmt.Fprintf(GinkgoWriter, "Failed to get curl-metrics logs: %s", err)
			}

			By("Fetching controller manager pod description")
			cmd = exec.Command("kubectl", "describe", "pod", controllerPodName, "-n", namespace)
			podDescription, err := utils.Run(cmd)
			if err == nil {
				fmt.Println("Pod description:\n", podDescription)
			} else {
				fmt.Println("Failed to describe controller pod")
			}
		}
	})

	SetDefaultEventuallyTimeout(2 * time.Minute)
	SetDefaultEventuallyPollingInterval(time.Second)

	Context("Manager", func() {
		It("should run successfully", func() {
			By("validating that the controller-manager pod is running as expected")
			verifyControllerUp := func(g Gomega) {
				// Get the name of the controller-manager pod
				cmd := exec.Command("kubectl", "get",
					"pods", "-l", "control-plane=controller-manager",
					"-o", "go-template={{ range .items }}"+
						"{{ if not .metadata.deletionTimestamp }}"+
						"{{ .metadata.name }}"+
						"{{ \"\\n\" }}{{ end }}{{ end }}",
					"-n", namespace,
				)

				podOutput, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred(), "Failed to retrieve controller-manager pod information")
				podNames := utils.GetNonEmptyLines(podOutput)
				g.Expect(podNames).To(HaveLen(1), "expected 1 controller pod running")
				controllerPodName = podNames[0]
				g.Expect(controllerPodName).To(ContainSubstring("controller-manager"))

				// Validate the pod's status
				cmd = exec.Command("kubectl", "get",
					"pods", controllerPodName, "-o", "jsonpath={.status.phase}",
					"-n", namespace,
				)
				output, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(output).To(Equal("Running"), "Incorrect controller-manager pod status")
			}
			Eventually(verifyControllerUp).Should(Succeed())
		})

		It("should ensure the metrics endpoint is serving metrics", func() {
			By("creating a ClusterRoleBinding for the service account to allow access to metrics")
			cmd := exec.Command("kubectl", "create", "clusterrolebinding", metricsRoleBindingName,
				"--clusterrole=headscale-operator-metrics-reader",
				fmt.Sprintf("--serviceaccount=%s:%s", namespace, serviceAccountName),
			)
			_, err := utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred(), "Failed to create ClusterRoleBinding")

			By("validating that the metrics service is available")
			cmd = exec.Command("kubectl", "get", "service", metricsServiceName, "-n", namespace)
			_, err = utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred(), "Metrics service should exist")

			By("getting the service account token")
			token, err := serviceAccountToken()
			Expect(err).NotTo(HaveOccurred())
			Expect(token).NotTo(BeEmpty())

			By("ensuring the controller pod is ready")
			verifyControllerPodReady := func(g Gomega) {
				cmd := exec.Command("kubectl", "get", "pod", controllerPodName, "-n", namespace,
					"-o", "jsonpath={.status.conditions[?(@.type=='Ready')].status}")
				output, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(output).To(Equal("True"), "Controller pod not ready")
			}
			Eventually(verifyControllerPodReady, 3*time.Minute, time.Second).Should(Succeed())

			By("verifying that the controller manager is serving the metrics server")
			verifyMetricsServerStarted := func(g Gomega) {
				cmd := exec.Command("kubectl", "logs", controllerPodName, "-n", namespace)
				output, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(output).To(ContainSubstring("Serving metrics server"),
					"Metrics server not yet started")
			}
			Eventually(verifyMetricsServerStarted, 3*time.Minute, time.Second).Should(Succeed())

			// +kubebuilder:scaffold:e2e-metrics-webhooks-readiness

			By("creating the curl-metrics pod to access the metrics endpoint")
			cmd = exec.Command("kubectl", "run", "curl-metrics", "--restart=Never",
				"--namespace", namespace,
				"--image=curlimages/curl:latest",
				"--overrides",
				fmt.Sprintf(`{
					"spec": {
						"containers": [{
							"name": "curl",
							"image": "curlimages/curl:latest",
							"command": ["/bin/sh", "-c"],
							"args": [
								"for i in $(seq 1 30); do curl -v -k -H 'Authorization: Bearer %s' https://%s.%s.svc.cluster.local:8443/metrics && exit 0 || sleep 2; done; exit 1"
							],
							"securityContext": {
								"readOnlyRootFilesystem": true,
								"allowPrivilegeEscalation": false,
								"capabilities": {
									"drop": ["ALL"]
								},
								"runAsNonRoot": true,
								"runAsUser": 1000,
								"seccompProfile": {
									"type": "RuntimeDefault"
								}
							}
						}],
						"serviceAccountName": "%s"
					}
				}`, token, metricsServiceName, namespace, serviceAccountName))
			_, err = utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred(), "Failed to create curl-metrics pod")

			By("waiting for the curl-metrics pod to complete.")
			verifyCurlUp := func(g Gomega) {
				cmd := exec.Command("kubectl", "get", "pods", "curl-metrics",
					"-o", "jsonpath={.status.phase}",
					"-n", namespace)
				output, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(output).To(Equal("Succeeded"), "curl pod in wrong status")
			}
			Eventually(verifyCurlUp, 5*time.Minute).Should(Succeed())

			By("getting the metrics by checking curl-metrics logs")
			verifyMetricsAvailable := func(g Gomega) {
				metricsOutput, err := getMetricsOutput()
				g.Expect(err).NotTo(HaveOccurred(), "Failed to retrieve logs from curl pod")
				g.Expect(metricsOutput).NotTo(BeEmpty())
				g.Expect(metricsOutput).To(ContainSubstring("< HTTP/1.1 200 OK"))
			}
			Eventually(verifyMetricsAvailable, 2*time.Minute).Should(Succeed())
		})

		// +kubebuilder:scaffold:e2e-webhooks-checks
	})

	Context("Headscale CR", func() {
		const headscaleName = "e2e-headscale"

		AfterEach(func() {
			By("cleaning up Headscale CR")
			cmd := exec.Command("kubectl", "delete", "headscale", headscaleName,
				"-n", testNamespace, "--ignore-not-found=true", "--timeout=60s")
			_, _ = utils.Run(cmd)
		})

		It("should deploy a Headscale instance and become ready", func() {
			By("creating a Headscale CR")
			headscaleYAML := fmt.Sprintf(`
apiVersion: headscale.infrado.cloud/v1beta1
kind: Headscale
metadata:
  name: %s
  namespace: %s
spec:
  version: "v0.28.0"
  replicas: 1
  config:
    server_url: http://headscale.local
    grpc_allow_insecure: true
    derp:
      server:
        enabled: false
    disable_check_updates: true
    database:
      type: sqlite
    dns:
      magic_dns: false
  api_key:
    auto_manage: true
    secret_name: "%s-api-key"
    expiration: "24h"
    rotation_buffer: "1h"
`, headscaleName, testNamespace, headscaleName)

			cmd := exec.Command("kubectl", "apply", "-f", "-")
			cmd.Stdin = strings.NewReader(headscaleYAML)
			_, err := utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred(), "Failed to create Headscale CR")

			By("verifying the Headscale StatefulSet is created")
			verifyStatefulSet := func(g Gomega) {
				cmd := exec.Command("kubectl", "get", "statefulset", "headscale",
					"-n", testNamespace, "-o", "jsonpath={.status.readyReplicas}")
				output, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(output).To(Equal("1"), "Headscale StatefulSet not ready")
			}
			Eventually(verifyStatefulSet, 3*time.Minute, 2*time.Second).Should(Succeed())

			By("verifying the Headscale CR status becomes Ready")
			verifyReady := func(g Gomega) {
				cmd := exec.Command("kubectl", "get", "headscale", headscaleName,
					"-n", testNamespace,
					"-o", "jsonpath={.status.conditions[?(@.type=='Ready')].status}")
				output, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(output).To(Equal("True"), "Headscale CR not ready")
			}
			Eventually(verifyReady, 3*time.Minute, 2*time.Second).Should(Succeed())

			By("verifying the API key secret is created")
			verifyAPIKeySecret := func(g Gomega) {
				cmd := exec.Command("kubectl", "get", "secret",
					fmt.Sprintf("%s-api-key", headscaleName),
					"-n", testNamespace, "-o", "jsonpath={.data.api-key}")
				output, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(output).NotTo(BeEmpty(), "API key secret not created or empty")
			}
			Eventually(verifyAPIKeySecret, 2*time.Minute, 2*time.Second).Should(Succeed())
		})
	})

	Context("HeadscaleUser CR", func() {
		const headscaleName = "e2e-headscale-user"
		const userName = "e2e-test-user"

		BeforeEach(func() {
			By("creating a Headscale instance for user tests")
			headscaleYAML := fmt.Sprintf(`
apiVersion: headscale.infrado.cloud/v1beta1
kind: Headscale
metadata:
  name: %s
  namespace: %s
spec:
  version: "v0.28.0"
  replicas: 1
  config:
    server_url: http://headscale.local
    grpc_allow_insecure: true
    derp:
      server:
        enabled: false
    disable_check_updates: true
    database:
      type: sqlite
    dns:
      magic_dns: false
  api_key:
    auto_manage: true
    expiration: "24h"
    rotation_buffer: "1h"
`, headscaleName, testNamespace)

			cmd := exec.Command("kubectl", "apply", "-f", "-")
			cmd.Stdin = strings.NewReader(headscaleYAML)
			_, err := utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred())

			By("waiting for Headscale to be ready")
			verifyReady := func(g Gomega) {
				cmd := exec.Command("kubectl", "get", "headscale", headscaleName,
					"-n", testNamespace,
					"-o", "jsonpath={.status.conditions[?(@.type=='Ready')].status}")
				output, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(output).To(Equal("True"))
			}
			Eventually(verifyReady, 3*time.Minute, 2*time.Second).Should(Succeed())
		})

		AfterEach(func() {
			By("cleaning up HeadscaleUser CR")
			cmd := exec.Command("kubectl", "delete", "headscaleuser", userName,
				"-n", testNamespace, "--ignore-not-found=true", "--timeout=60s")
			_, _ = utils.Run(cmd)

			By("cleaning up Headscale CR")
			cmd = exec.Command("kubectl", "delete", "headscale", headscaleName,
				"-n", testNamespace, "--ignore-not-found=true", "--timeout=60s")
			_, _ = utils.Run(cmd)
		})

		It("should create a user in Headscale and populate status.userId", func() {
			By("creating a HeadscaleUser CR")
			userYAML := fmt.Sprintf(`
apiVersion: headscale.infrado.cloud/v1beta1
kind: HeadscaleUser
metadata:
  name: %s
  namespace: %s
spec:
  headscaleRef: %s
  username: %s
  displayName: "E2E Test User"
  email: "e2e@test.local"
`, userName, testNamespace, headscaleName, userName)

			cmd := exec.Command("kubectl", "apply", "-f", "-")
			cmd.Stdin = strings.NewReader(userYAML)
			_, err := utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred(), "Failed to create HeadscaleUser CR")

			By("verifying the HeadscaleUser CR status becomes Ready with userId")
			verifyUserReady := func(g Gomega) {
				cmd := exec.Command("kubectl", "get", "headscaleuser", userName,
					"-n", testNamespace,
					"-o", "jsonpath={.status.conditions[?(@.type=='Ready')].status}")
				output, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(output).To(Equal("True"), "HeadscaleUser CR not ready")
			}
			Eventually(verifyUserReady, 2*time.Minute, 2*time.Second).Should(Succeed())

			By("verifying the userId is populated in status")
			verifyUserID := func(g Gomega) {
				cmd := exec.Command("kubectl", "get", "headscaleuser", userName,
					"-n", testNamespace, "-o", "jsonpath={.status.userId}")
				output, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(output).NotTo(BeEmpty(), "userId should be populated")
			}
			Eventually(verifyUserID, 30*time.Second, 2*time.Second).Should(Succeed())

			By("verifying the user exists in Headscale")
			verifyUserInHeadscale := func(g Gomega) {
				pod, err := getHeadscalePodName(testNamespace, "headscale")
				g.Expect(err).NotTo(HaveOccurred())

				// exec into headscale pod and check user list
				cmd := exec.Command("kubectl", "exec", "-n", testNamespace, pod, "--", "headscale", "users", "list", "-o", "json")
				output, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred())
				// We expect the username to be present in the output
				g.Expect(output).To(ContainSubstring(fmt.Sprintf(`"name": "%s"`, userName)))
			}
			Eventually(verifyUserInHeadscale, 1*time.Minute, 2*time.Second).Should(Succeed())

			By("deleting the HeadscaleUser CR")
			cmd = exec.Command("kubectl", "delete", "headscaleuser", userName, "-n", testNamespace)
			_, err = utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred())

			By("verifying the user is removed from Headscale")
			verifyUserRemovedFromHeadscale := func(g Gomega) {
				// We need to re-fetch pod name just in case, but it should be same
				pod, err := getHeadscalePodName(testNamespace, "headscale")
				g.Expect(err).NotTo(HaveOccurred())

				cmd := exec.Command("kubectl", "exec", "-n", testNamespace, pod, "--", "headscale", "users", "list", "-o", "json")
				output, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(output).NotTo(ContainSubstring(fmt.Sprintf(`"name": "%s"`, userName)))
			}
			Eventually(verifyUserRemovedFromHeadscale, 1*time.Minute, 2*time.Second).Should(Succeed())
		})
	})

	Context("HeadscalePreAuthKey CR", func() {
		const headscaleName = "e2e-headscale-pak"
		const userName = "e2e-pak-user"
		const preAuthKeyName = "e2e-preauth-key"

		BeforeEach(func() {
			By("creating a Headscale instance")
			headscaleYAML := fmt.Sprintf(`
apiVersion: headscale.infrado.cloud/v1beta1
kind: Headscale
metadata:
  name: %s
  namespace: %s
spec:
  version: "v0.28.0"
  replicas: 1
  config:
    server_url: http://headscale.local
    grpc_allow_insecure: true
    derp:
      server:
        enabled: false
    disable_check_updates: true
    database:
      type: sqlite
    dns:
      magic_dns: false
  api_key:
    auto_manage: true
    expiration: "24h"
    rotation_buffer: "1h"
`, headscaleName, testNamespace)

			cmd := exec.Command("kubectl", "apply", "-f", "-")
			cmd.Stdin = strings.NewReader(headscaleYAML)
			_, err := utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred())

			By("waiting for Headscale to be ready")
			verifyReady := func(g Gomega) {
				cmd := exec.Command("kubectl", "get", "headscale", headscaleName,
					"-n", testNamespace,
					"-o", "jsonpath={.status.conditions[?(@.type=='Ready')].status}")
				output, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(output).To(Equal("True"))
			}
			Eventually(verifyReady, 3*time.Minute, 2*time.Second).Should(Succeed())

			By("creating a HeadscaleUser for the preauth key")
			userYAML := fmt.Sprintf(`
apiVersion: headscale.infrado.cloud/v1beta1
kind: HeadscaleUser
metadata:
  name: %s
  namespace: %s
spec:
  headscaleRef: %s
  username: %s
`, userName, testNamespace, headscaleName, userName)

			cmd = exec.Command("kubectl", "apply", "-f", "-")
			cmd.Stdin = strings.NewReader(userYAML)
			_, err = utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred())

			By("waiting for HeadscaleUser to be ready")
			verifyUserReady := func(g Gomega) {
				cmd := exec.Command("kubectl", "get", "headscaleuser", userName,
					"-n", testNamespace,
					"-o", "jsonpath={.status.conditions[?(@.type=='Ready')].status}")
				output, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(output).To(Equal("True"))
			}
			Eventually(verifyUserReady, 2*time.Minute, 2*time.Second).Should(Succeed())
		})

		AfterEach(func() {
			By("cleaning up HeadscalePreAuthKey CR")
			cmd := exec.Command("kubectl", "delete", "headscalepreauthkey", preAuthKeyName,
				"-n", testNamespace, "--ignore-not-found=true", "--timeout=60s")
			_, _ = utils.Run(cmd)

			By("cleaning up HeadscaleUser CR")
			cmd = exec.Command("kubectl", "delete", "headscaleuser", userName,
				"-n", testNamespace, "--ignore-not-found=true", "--timeout=60s")
			_, _ = utils.Run(cmd)

			By("cleaning up Headscale CR")
			cmd = exec.Command("kubectl", "delete", "headscale", headscaleName,
				"-n", testNamespace, "--ignore-not-found=true", "--timeout=60s")
			_, _ = utils.Run(cmd)
		})

		It("should create a preauth key and store it in a secret", func() {
			By("creating a HeadscalePreAuthKey CR")
			preAuthKeyYAML := fmt.Sprintf(`
apiVersion: headscale.infrado.cloud/v1beta1
kind: HeadscalePreAuthKey
metadata:
  name: %s
  namespace: %s
spec:
  headscaleRef: %s
  headscaleUserRef: %s
  expiration: "1h"
  reusable: false
  ephemeral: false
  tags:
    - "tag:e2e"
`, preAuthKeyName, testNamespace, headscaleName, userName)

			cmd := exec.Command("kubectl", "apply", "-f", "-")
			cmd.Stdin = strings.NewReader(preAuthKeyYAML)
			_, err := utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred(), "Failed to create HeadscalePreAuthKey CR")

			By("verifying the HeadscalePreAuthKey CR status becomes Ready")
			verifyKeyReady := func(g Gomega) {
				cmd := exec.Command("kubectl", "get", "headscalepreauthkey", preAuthKeyName,
					"-n", testNamespace,
					"-o", "jsonpath={.status.conditions[?(@.type=='Ready')].status}")
				output, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(output).To(Equal("True"), "HeadscalePreAuthKey CR not ready")
			}
			Eventually(verifyKeyReady, 2*time.Minute, 2*time.Second).Should(Succeed())

			By("verifying the keyId is populated in status")
			verifyKeyID := func(g Gomega) {
				cmd := exec.Command("kubectl", "get", "headscalepreauthkey", preAuthKeyName,
					"-n", testNamespace, "-o", "jsonpath={.status.keyId}")
				output, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(output).NotTo(BeEmpty(), "keyId should be populated")
			}
			Eventually(verifyKeyID, 30*time.Second, 2*time.Second).Should(Succeed())

			By("verifying the preauth key secret is created with the key")
			verifySecret := func(g Gomega) {
				cmd := exec.Command("kubectl", "get", "secret", preAuthKeyName,
					"-n", testNamespace, "-o", "jsonpath={.data.key}")
				output, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(output).NotTo(BeEmpty(), "Preauth key secret should contain the key")
			}
			Eventually(verifySecret, 30*time.Second, 2*time.Second).Should(Succeed())

			By("verifying the preauth key exists in Headscale")
			verifyKeyInHeadscale := func(g Gomega) {
				pod, err := getHeadscalePodName(testNamespace, "headscale")
				g.Expect(err).NotTo(HaveOccurred())

				cmd := exec.Command("kubectl", "exec", "-n", testNamespace, pod, "--", "headscale", "preauthkeys", "list", "-o", "json")
				output, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred())

				g.Expect(output).To(ContainSubstring(`"name": "` + userName + `"`))
			}
			Eventually(verifyKeyInHeadscale, 1*time.Minute, 2*time.Second).Should(Succeed())

			By("deleting the HeadscalePreAuthKey CR")
			cmd = exec.Command("kubectl", "delete", "headscalepreauthkey", preAuthKeyName,
				"-n", testNamespace, "--timeout=60s")
			_, err = utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred(), "Failed to delete HeadscalePreAuthKey CR")

			By("verifying the preauth key is removed from Headscale")
			verifyKeyRemovedFromHeadscale := func(g Gomega) {
				pod, err := getHeadscalePodName(testNamespace, "headscale")
				g.Expect(err).NotTo(HaveOccurred())

				cmd := exec.Command("kubectl", "exec", "-n", testNamespace, pod, "--", "headscale", "preauthkeys", "list", "-o", "json")
				output, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred())

				// Should generally be empty or not contain the key we created.
				if strings.Contains(output, `"id":`) {
					g.Expect(output).NotTo(ContainSubstring(`"name": "` + userName + `"`))
				}
			}
			Eventually(verifyKeyRemovedFromHeadscale, 1*time.Minute, 2*time.Second).Should(Succeed())
		})

		It("should delete preauth key in Headscale when CR is deleted", func() {
			By("creating a HeadscalePreAuthKey CR")
			preAuthKeyYAML := fmt.Sprintf(`
apiVersion: headscale.infrado.cloud/v1beta1
kind: HeadscalePreAuthKey
metadata:
  name: %s
  namespace: %s
spec:
  headscaleRef: %s
  headscaleUserRef: %s
  expiration: "1h"
`, preAuthKeyName, testNamespace, headscaleName, userName)

			cmd := exec.Command("kubectl", "apply", "-f", "-")
			cmd.Stdin = strings.NewReader(preAuthKeyYAML)
			_, err := utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred())

			By("waiting for the preauth key to be ready")
			verifyKeyReady := func(g Gomega) {
				cmd := exec.Command("kubectl", "get", "headscalepreauthkey", preAuthKeyName,
					"-n", testNamespace,
					"-o", "jsonpath={.status.conditions[?(@.type=='Ready')].status}")
				output, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(output).To(Equal("True"))
			}
			Eventually(verifyKeyReady, 2*time.Minute, 2*time.Second).Should(Succeed())

			By("verifying the preauth key exists in Headscale")
			verifyKeyInHeadscale := func(g Gomega) {
				pod, err := getHeadscalePodName(testNamespace, "headscale")
				g.Expect(err).NotTo(HaveOccurred())
				cmd := exec.Command("kubectl", "exec", "-n", testNamespace, pod, "--", "headscale", "preauthkeys", "list", "-o", "json")
				output, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(output).To(ContainSubstring(`"name": "` + userName + `"`))
			}
			Eventually(verifyKeyInHeadscale, 1*time.Minute, 2*time.Second).Should(Succeed())

			By("deleting the HeadscalePreAuthKey CR")
			cmd = exec.Command("kubectl", "delete", "headscalepreauthkey", preAuthKeyName,
				"-n", testNamespace, "--timeout=60s")
			_, err = utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred(), "Failed to delete HeadscalePreAuthKey CR")

			By("verifying the HeadscalePreAuthKey CR is deleted")
			verifyDeleted := func(g Gomega) {
				cmd := exec.Command("kubectl", "get", "headscalepreauthkey", preAuthKeyName,
					"-n", testNamespace)
				_, err := utils.Run(cmd)
				g.Expect(err).To(HaveOccurred(), "HeadscalePreAuthKey CR should be deleted")
			}
			Eventually(verifyDeleted, 30*time.Second, 2*time.Second).Should(Succeed())

			By("verifying the preauth key secret is also deleted")
			verifySecretDeleted := func(g Gomega) {
				cmd := exec.Command("kubectl", "get", "secret", preAuthKeyName, "-n", testNamespace)
				_, err := utils.Run(cmd)
				g.Expect(err).To(HaveOccurred(), "Preauth key secret should be deleted")
			}
			Eventually(verifySecretDeleted, 30*time.Second, 2*time.Second).Should(Succeed())

			By("verifying the preauth key is removed from Headscale")
			verifyKeyRemovedFromHeadscale := func(g Gomega) {
				pod, err := getHeadscalePodName(testNamespace, "headscale")
				g.Expect(err).NotTo(HaveOccurred())
				cmd := exec.Command("kubectl", "exec", "-n", testNamespace, pod, "--", "headscale", "preauthkeys", "list", "-o", "json")
				output, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred())
				if strings.Contains(output, `"id":`) {
					g.Expect(output).NotTo(ContainSubstring(`"id":`))
				}
			}
			Eventually(verifyKeyRemovedFromHeadscale, 1*time.Minute, 2*time.Second).Should(Succeed())
		})
	})

	Context("Resource Dependencies", func() {
		const headscaleName = "e2e-headscale-deps"
		const userName = "e2e-deps-user"
		const preAuthKeyName = "e2e-deps-pak"

		AfterEach(func() {
			By("cleaning up all test resources")
			cmd := exec.Command("kubectl", "delete", "headscalepreauthkey", preAuthKeyName,
				"-n", testNamespace, "--ignore-not-found=true", "--timeout=60s")
			_, _ = utils.Run(cmd)
			cmd = exec.Command("kubectl", "delete", "headscaleuser", userName,
				"-n", testNamespace, "--ignore-not-found=true", "--timeout=60s")
			_, _ = utils.Run(cmd)
			cmd = exec.Command("kubectl", "delete", "headscale", headscaleName,
				"-n", testNamespace, "--ignore-not-found=true", "--timeout=60s")
			_, _ = utils.Run(cmd)
		})

		It("should cascade delete PreAuthKey when User is deleted", func() {
			By("creating Headscale instance")
			headscaleYAML := fmt.Sprintf(`
apiVersion: headscale.infrado.cloud/v1beta1
kind: Headscale
metadata:
  name: %s
  namespace: %s
spec:
  version: "v0.28.0"
  replicas: 1
  config:
    server_url: http://headscale.local
    grpc_allow_insecure: true
    derp:
      server:
        enabled: false
    disable_check_updates: true
    database:
      type: sqlite
    dns:
      magic_dns: false
  api_key:
    auto_manage: true
    expiration: "24h"
    rotation_buffer: "1h"
`, headscaleName, testNamespace)

			cmd := exec.Command("kubectl", "apply", "-f", "-")
			cmd.Stdin = strings.NewReader(headscaleYAML)
			_, err := utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred())

			By("waiting for Headscale to be ready")
			verifyReady := func(g Gomega) {
				cmd := exec.Command("kubectl", "get", "headscale", headscaleName,
					"-n", testNamespace,
					"-o", "jsonpath={.status.conditions[?(@.type=='Ready')].status}")
				output, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(output).To(Equal("True"))
			}
			Eventually(verifyReady, 3*time.Minute, 2*time.Second).Should(Succeed())

			By("creating HeadscaleUser")
			userYAML := fmt.Sprintf(`
apiVersion: headscale.infrado.cloud/v1beta1
kind: HeadscaleUser
metadata:
  name: %s
  namespace: %s
spec:
  headscaleRef: %s
  username: %s
`, userName, testNamespace, headscaleName, userName)

			cmd = exec.Command("kubectl", "apply", "-f", "-")
			cmd.Stdin = strings.NewReader(userYAML)
			_, err = utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred())

			By("waiting for HeadscaleUser to be ready")
			verifyUserReady := func(g Gomega) {
				cmd := exec.Command("kubectl", "get", "headscaleuser", userName,
					"-n", testNamespace,
					"-o", "jsonpath={.status.conditions[?(@.type=='Ready')].status}")
				output, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(output).To(Equal("True"))
			}
			Eventually(verifyUserReady, 2*time.Minute, 2*time.Second).Should(Succeed())

			By("creating HeadscalePreAuthKey referencing the user")
			preAuthKeyYAML := fmt.Sprintf(`
apiVersion: headscale.infrado.cloud/v1beta1
kind: HeadscalePreAuthKey
metadata:
  name: %s
  namespace: %s
spec:
  headscaleRef: %s
  headscaleUserRef: %s
  expiration: "1h"
`, preAuthKeyName, testNamespace, headscaleName, userName)

			cmd = exec.Command("kubectl", "apply", "-f", "-")
			cmd.Stdin = strings.NewReader(preAuthKeyYAML)
			_, err = utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred())

			By("waiting for HeadscalePreAuthKey to be ready")
			verifyKeyReady := func(g Gomega) {
				cmd := exec.Command("kubectl", "get", "headscalepreauthkey", preAuthKeyName,
					"-n", testNamespace,
					"-o", "jsonpath={.status.conditions[?(@.type=='Ready')].status}")
				output, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(output).To(Equal("True"))
			}
			Eventually(verifyKeyReady, 2*time.Minute, 2*time.Second).Should(Succeed())

			By("verifying the PreAuthKey has owner reference to User")
			verifyOwnerRef := func(g Gomega) {
				cmd := exec.Command("kubectl", "get", "headscalepreauthkey", preAuthKeyName,
					"-n", testNamespace,
					"-o", "jsonpath={.metadata.ownerReferences[0].name}")
				output, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(output).To(Equal(userName), "PreAuthKey should have owner reference to User")
			}
			Eventually(verifyOwnerRef, 30*time.Second, 2*time.Second).Should(Succeed())

			By("deleting the HeadscaleUser")
			cmd = exec.Command("kubectl", "delete", "headscaleuser", userName,
				"-n", testNamespace, "--timeout=120s")
			_, err = utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred(), "Failed to delete HeadscaleUser")

			By("verifying the HeadscalePreAuthKey is also deleted (cascade)")
			verifyKeyDeleted := func(g Gomega) {
				cmd := exec.Command("kubectl", "get", "headscalepreauthkey", preAuthKeyName,
					"-n", testNamespace)
				_, err := utils.Run(cmd)
				g.Expect(err).To(HaveOccurred(), "HeadscalePreAuthKey should be deleted via cascade")
			}
			Eventually(verifyKeyDeleted, 60*time.Second, 2*time.Second).Should(Succeed())
		})
	})
})

// serviceAccountToken returns a token for the specified service account in the given namespace.
// It uses the Kubernetes TokenRequest API to generate a token by directly sending a request
// and parsing the resulting token from the API response.
func serviceAccountToken() (string, error) {
	const tokenRequestRawString = `{
		"apiVersion": "authentication.k8s.io/v1",
		"kind": "TokenRequest"
	}`

	// Temporary file to store the token request
	secretName := fmt.Sprintf("%s-token-request", serviceAccountName)
	tokenRequestFile := filepath.Join("/tmp", secretName)
	err := os.WriteFile(tokenRequestFile, []byte(tokenRequestRawString), os.FileMode(0o644))
	if err != nil {
		return "", err
	}

	var out string
	verifyTokenCreation := func(g Gomega) {
		// Execute kubectl command to create the token
		cmd := exec.Command("kubectl", "create", "--raw", fmt.Sprintf(
			"/api/v1/namespaces/%s/serviceaccounts/%s/token",
			namespace,
			serviceAccountName,
		), "-f", tokenRequestFile)

		output, err := cmd.CombinedOutput()
		g.Expect(err).NotTo(HaveOccurred())

		// Parse the JSON output to extract the token
		var token tokenRequest
		err = json.Unmarshal(output, &token)
		g.Expect(err).NotTo(HaveOccurred())

		out = token.Status.Token
	}
	Eventually(verifyTokenCreation).Should(Succeed())

	return out, err
}

// getMetricsOutput retrieves and returns the logs from the curl pod used to access the metrics endpoint.
func getMetricsOutput() (string, error) {
	By("getting the curl-metrics logs")
	cmd := exec.Command("kubectl", "logs", "curl-metrics", "-n", namespace)
	return utils.Run(cmd)
}

// getHeadscalePodName returns the name of the first running pod for the given deployment.
// It searches for pods starting with the deployment name.
func getHeadscalePodName(namespace, deploymentName string) (string, error) {
	cmd := exec.Command("kubectl", "get", "pods",
		"-n", namespace,
		"-o", "go-template={{ range .items }}{{ if not .metadata.deletionTimestamp }}{{ .metadata.name }} {{ end }}{{ end }}")
	output, err := utils.Run(cmd)
	if err != nil {
		return "", err
	}
	pods := strings.Fields(output)
	for _, pod := range pods {
		if strings.HasPrefix(pod, deploymentName+"-") {
			return pod, nil
		}
	}
	return "", fmt.Errorf("headscale pod not found for deployment %s", deploymentName)
}

// tokenRequest is a simplified representation of the Kubernetes TokenRequest API response,
// containing only the token field that we need to extract.
type tokenRequest struct {
	Status struct {
		Token string `json:"token"`
	} `json:"status"`
}
