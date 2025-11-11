package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	coverageclient "github.com/psturc/go-coverage-http/client"
)

func TestE2E(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Coverage Collection E2E Suite")
}

const (
	defaultNamespace   = "coverage-demo"
	labelSelector      = "app=coverage-demo"
	targetPort         = 9095 // Coverage server port
	defaultCoverageDir = "./coverage-output"
	defaultAppUrl      = "http://127.0.0.1:8000"
	// Set source directory to parent directory (project root)
	// Since tests run from ./test/, we need to go up one level
	projectRoot = ".."
)

var (
	namespace      string
	appUrl         string
	podName        string
	coverageDir    string
	coverageClient *coverageclient.CoverageClient
)

var _ = BeforeSuite(func() {
	var err error

	// Get namespace from environment or use default
	namespace = os.Getenv("APP_NAMESPACE")
	if namespace == "" {
		namespace = defaultNamespace
	}
	GinkgoWriter.Printf("📍 Using namespace: %s\n", namespace)

	// Get app URL from environment or use default
	appUrl = os.Getenv("APP_URL")
	if appUrl == "" {
		appUrl = defaultAppUrl
	}
	GinkgoWriter.Printf("📍 App URL: %s\n", appUrl)

	// Get coverage directory from environment or use default
	coverageDir = os.Getenv("COVERAGE_OUTPUT_DIR")
	if coverageDir == "" {
		coverageDir = defaultCoverageDir
	}
	GinkgoWriter.Printf("📍 Coverage directory: %s\n", coverageDir)

	// Initialize coverage client
	coverageClient, err = coverageclient.NewClient(namespace, coverageDir)
	Expect(err).NotTo(HaveOccurred(), "Failed to create coverage client")

	coverageClient.SetPathRemapping(false)

	coverageClient.SetSourceDirectory(projectRoot)
	GinkgoWriter.Printf("✅ Coverage client initialized (source dir: %s)\n", projectRoot)

	// Discover pod using label selector
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	podName, err = coverageClient.GetPodNameWithContext(ctx, labelSelector)
	Expect(err).NotTo(HaveOccurred(), "Failed to discover pod")
})

var _ = Describe("Application E2E Tests", func() {
	It("should respond to health checks", func() {
		resp, err := http.Get(appUrl + "/health")
		Expect(err).NotTo(HaveOccurred())
		defer resp.Body.Close()
		body, err := io.ReadAll(resp.Body)
		Expect(err).NotTo(HaveOccurred())
		Expect(string(body)).To(ContainSubstring("healthy"))
	})

	It("should handle greet requests", func() {
		resp, err := http.Get(appUrl + "/greet?name=Test")
		Expect(err).NotTo(HaveOccurred())
		defer resp.Body.Close()
		body, err := io.ReadAll(resp.Body)
		Expect(err).NotTo(HaveOccurred())
		Expect(string(body)).To(ContainSubstring("Hello, Test!"))
	})

	It("should handle greet request with long name", func() {
		name := strings.Repeat("VeryLongName", 10)
		resp, err := http.Get(appUrl + "/greet?name=" + name)
		Expect(err).NotTo(HaveOccurred())
		defer resp.Body.Close()
		body, err := io.ReadAll(resp.Body)
		Expect(err).NotTo(HaveOccurred())
		Expect(string(body)).To(ContainSubstring("Hello, " + name + "! Wow you have a long name."))
	})

	It("should handle greet request with empty name", func() {
		resp, err := http.Get(appUrl + "/greet?name=")
		Expect(err).NotTo(HaveOccurred())
		defer resp.Body.Close()
		body, err := io.ReadAll(resp.Body)
		Expect(err).NotTo(HaveOccurred())
		Expect(string(body)).To(ContainSubstring("Hello, stranger!"))
	})

	It("should handle calculate requests", func() {
		resp, err := http.Get(appUrl + "/calculate?a=10&b=5")
		Expect(err).NotTo(HaveOccurred())
		defer resp.Body.Close()
		body, err := io.ReadAll(resp.Body)
		Expect(err).NotTo(HaveOccurred())
		Expect(string(body)).To(ContainSubstring("15"))
	})

	It("should handle calculate requests with big numbers", func() {
		resp, err := http.Get(appUrl + "/calculate?a=1001&b=1001")
		Expect(err).NotTo(HaveOccurred())
		defer resp.Body.Close()
		body, err := io.ReadAll(resp.Body)
		Expect(err).NotTo(HaveOccurred())
		Expect(string(body)).To(ContainSubstring("2002"))
	})
})

var _ = AfterSuite(func() {
	By("Collecting coverage data from pod")
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	testName := "e2e-tests"

	// Collect coverage from pod (this also saves metadata.json)
	// The client will try to auto-detect which container is serving coverage on port 9095
	// If you know the container name, you can use: CollectCoverageFromPodWithContainer(ctx, podName, "app", testName, targetPort)
	err := coverageClient.CollectCoverageFromPod(ctx, podName, testName, targetPort)
	Expect(err).NotTo(HaveOccurred(), "Failed to collect coverage")

	// Read and display pod metadata
	By("Reading pod metadata")
	metadataPath := filepath.Join(coverageDir, testName, "metadata.json")
	if metadataData, err := os.ReadFile(metadataPath); err == nil {
		var metadata map[string]interface{}
		if err := json.Unmarshal(metadataData, &metadata); err == nil {
			GinkgoWriter.Println("\n📋 Pod Metadata:")
			GinkgoWriter.Printf("  Pod Name: %v\n", metadata["pod_name"])
			GinkgoWriter.Printf("  Namespace: %v\n", metadata["namespace"])
			GinkgoWriter.Printf("  Coverage Port: %v\n", metadata["coverage_port"])
			if container, ok := metadata["container"].(map[string]interface{}); ok {
				GinkgoWriter.Println("  Coverage Container:")
				GinkgoWriter.Printf("    Name: %v\n", container["name"])
				GinkgoWriter.Printf("    Image: %v\n", container["image"])
			}
			GinkgoWriter.Printf("  Collected At: %v\n", metadata["collected_at"])
		}
	} else {
		GinkgoWriter.Printf("⚠️  Failed to read metadata: %v\n", err)
	}

	GinkgoWriter.Println("\n✅ Coverage data collected successfully!")
	GinkgoWriter.Printf("📁 Coverage files in: %s/%s/\n", coverageDir, testName)

	// Push coverage artifact to OCI registry (only if enabled via environment variable)
	if os.Getenv("PUSH_COVERAGE_ARTIFACT") == "true" {
		By("Pushing coverage artifact to OCI registry")

		// Create a new context with longer timeout specifically for the push operation
		pushCtx, pushCancel := context.WithTimeout(context.Background(), 120*time.Second)
		defer pushCancel()

		pushOpts := coverageclient.PushCoverageArtifactOptions{
			Registry:     "quay.io",
			Repository:   "psturc/coverage-artifacts",
			Tag:          fmt.Sprintf("e2e-coverage-%s", time.Now().Format("20060102-150405")),
			ExpiresAfter: "1y",
			Title:        "Artifact for storing E2E coverage data",
		}

		artifactRef := fmt.Sprintf("%s/%s:%s", pushOpts.Registry, pushOpts.Repository, pushOpts.Tag)
		GinkgoWriter.Printf("   Target: %s\n", artifactRef)

		err = coverageClient.PushCoverageArtifact(pushCtx, testName, pushOpts)
		if err != nil {
			GinkgoWriter.Printf("\n⚠️  Failed to push coverage artifact: %v\n", err)
			GinkgoWriter.Println("   (This is non-fatal - coverage data is still saved locally)")
		} else {
			GinkgoWriter.Printf("\n✅ Coverage artifact pushed successfully!\n")
			GinkgoWriter.Printf("   Location: %s\n", artifactRef)

			// Write artifact reference to file for Tekton pipeline
			if artifactRefPath := os.Getenv("COVERAGE_ARTIFACT_REF_FILE"); artifactRefPath != "" {
				if err := os.WriteFile(artifactRefPath, []byte(artifactRef), 0644); err != nil {
					GinkgoWriter.Printf("⚠️  Failed to write artifact ref to %s: %v\n", artifactRefPath, err)
				} else {
					GinkgoWriter.Printf("📝 Artifact reference saved to: %s\n", artifactRefPath)
				}
			}
		}
	} else {
		GinkgoWriter.Println("\n💾 Coverage artifacts saved locally (OCI push disabled)")
		GinkgoWriter.Println("   Set PUSH_COVERAGE_ARTIFACT=true to enable pushing to registry")
	}
})
