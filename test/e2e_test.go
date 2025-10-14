package main

import (
	"context"
	"os/exec"
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
	namespace  = "coverage-demo"
	targetPort = 9095 // Coverage server port
)

var (
	podName        string
	coverageClient *coverageclient.CoverageClient
	coverageDir    = "./coverage-output"
)

var _ = BeforeSuite(func() {
	// Wait for pod to be ready
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "kubectl", "get", "pod", "-n", namespace,
		"-l", "app=coverage-demo", "-o", "jsonpath={.items[0].metadata.name}")
	output, err := cmd.Output()
	Expect(err).NotTo(HaveOccurred())

	podName = strings.TrimSpace(string(output))
	Expect(podName).NotTo(BeEmpty(), "Pod not found")

	GinkgoWriter.Printf("✅ Testing pod: %s\n", podName)

	// Initialize coverage client
	coverageClient, err = coverageclient.NewClient(namespace, coverageDir)
	Expect(err).NotTo(HaveOccurred(), "Failed to create coverage client")

	GinkgoWriter.Println("✅ Coverage client initialized")
})

var _ = Describe("Application E2E Tests", func() {
	It("should respond to health checks", func() {
		resp := execInPod("wget -q -O- http://localhost:8080/health")
		Expect(resp).To(ContainSubstring("healthy"))
	})

	It("should handle greet requests", func() {
		resp := execInPod("wget -q -O- 'http://localhost:8080/greet?name=Test'")
		Expect(resp).To(ContainSubstring("Hello, Test!"))
	})

	It("should handle calculate requests", func() {
		resp := execInPod("wget -q -O- http://localhost:8080/calculate")
		Expect(resp).To(ContainSubstring("result"))
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

	// Generate text report
	By("Generating coverage report")
	err = coverageClient.GenerateCoverageReport(testName)
	Expect(err).NotTo(HaveOccurred(), "Failed to generate coverage report")

	// Filter out coverage_server.go
	By("Filtering coverage report")
	err = coverageClient.FilterCoverageReport(testName)
	Expect(err).NotTo(HaveOccurred(), "Failed to filter coverage report")

	// Generate HTML report
	By("Generating HTML report")
	err = coverageClient.GenerateHTMLReport(testName)
	if err != nil {
		GinkgoWriter.Printf("⚠️  HTML report generation failed (source files may not be available): %v\n", err)
	}

	// Print coverage summary
	By("Printing coverage summary")
	err = coverageClient.PrintCoverageSummary(testName)
	if err != nil {
		GinkgoWriter.Printf("⚠️  Failed to print summary: %v\n", err)
	}

	GinkgoWriter.Println("\n✅ Coverage collection complete!")
	GinkgoWriter.Printf("📊 Reports in: %s/%s/\n", coverageDir, testName)
})

// execInPod executes a command in the pod and returns output
func execInPod(command string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "kubectl", "exec", "-n", namespace, podName, "--", "sh", "-c", command)
	output, err := cmd.CombinedOutput()
	Expect(err).NotTo(HaveOccurred(), "Command failed: %s", string(output))

	return string(output)
}
