package main

import (
	"context"
	"io"
	"net/http"
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
	namespace     = "coverage-demo"
	labelSelector = "app=coverage-demo"
	targetPort    = 9095 // Coverage server port
	coverageDir   = "./coverage-output"
	// Use 127.0.0.1 instead of localhost to explicitly use IPv4
	appUrl = "http://127.0.0.1:8000"
	// Set source directory to parent directory (project root)
	// Since tests run from ./test/, we need to go up one level
	projectRoot = ".."
)

var (
	podName        string
	coverageClient *coverageclient.CoverageClient
)

var _ = BeforeSuite(func() {
	var err error

	// Initialize coverage client
	coverageClient, err = coverageclient.NewClient(namespace, coverageDir)
	Expect(err).NotTo(HaveOccurred(), "Failed to create coverage client")

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

	It("should handle greet requests with 'unhuman' name", func() {
		resp, err := http.Get(appUrl + "/greet?name=X")
		Expect(err).NotTo(HaveOccurred())
		defer resp.Body.Close()
		body, err := io.ReadAll(resp.Body)
		Expect(err).NotTo(HaveOccurred())
		Expect(string(body)).To(ContainSubstring("Are you sure you are human?"))
	})

	It("should handle calculate requests", func() {
		resp, err := http.Get(appUrl + "/calculate")
		Expect(err).NotTo(HaveOccurred())
		defer resp.Body.Close()
		body, err := io.ReadAll(resp.Body)
		Expect(err).NotTo(HaveOccurred())
		Expect(string(body)).To(ContainSubstring("15"))
	})
})

var _ = AfterSuite(func() {
	By("Collecting coverage data from pod")
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	testName := "e2e-tests"

	// Collect coverage from pod
	err := coverageClient.CollectCoverageFromPod(ctx, podName, testName, targetPort)
	Expect(err).NotTo(HaveOccurred(), "Failed to collect coverage")

	// Process coverage reports (generate, filter, and create HTML)
	By("Processing coverage reports")
	err = coverageClient.ProcessCoverageReports(testName)
	Expect(err).NotTo(HaveOccurred(), "Failed to process coverage reports")

	// Print coverage summary
	By("Printing coverage summary")
	err = coverageClient.PrintCoverageSummary(testName)
	if err != nil {
		GinkgoWriter.Printf("⚠️  Failed to print summary: %v\n", err)
	}

	GinkgoWriter.Println("\n✅ Coverage collection complete!")
	GinkgoWriter.Printf("📊 Reports in: %s/%s/\n", coverageDir, testName)
})
