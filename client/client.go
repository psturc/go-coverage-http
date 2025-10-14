package coverageclient

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/tools/portforward"
	"k8s.io/client-go/transport/spdy"
)

// CoverageClient handles coverage collection from Kubernetes pods
type CoverageClient struct {
	clientset       *kubernetes.Clientset
	restConfig      *rest.Config
	namespace       string
	outputDir       string
	httpClient      *http.Client
	portForwardStop chan struct{}
	localPort       int
}

// CoverageResponse matches the server's response format
type CoverageResponse struct {
	MetaFilename     string `json:"meta_filename"`
	MetaData         string `json:"meta_data"`
	CountersFilename string `json:"counters_filename"`
	CountersData     string `json:"counters_data"`
	TestName         string `json:"test_name"`
	Timestamp        int64  `json:"timestamp"`
}

// NewClient creates a new coverage client for the given namespace
func NewClient(namespace, outputDir string) (*CoverageClient, error) {
	// Load kubeconfig
	kubeconfig := os.Getenv("KUBECONFIG")
	if kubeconfig == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, fmt.Errorf("get home dir: %w", err)
		}
		kubeconfig = filepath.Join(home, ".kube", "config")
	}

	// Build config from kubeconfig
	config, err := clientcmd.BuildConfigFromFlags("", kubeconfig)
	if err != nil {
		// Try in-cluster config
		config, err = rest.InClusterConfig()
		if err != nil {
			return nil, fmt.Errorf("build kubernetes config: %w", err)
		}
	}

	// Create clientset
	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		return nil, fmt.Errorf("create kubernetes client: %w", err)
	}

	// Create output directory if it doesn't exist
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return nil, fmt.Errorf("create output directory: %w", err)
	}

	return &CoverageClient{
		clientset:  clientset,
		restConfig: config,
		namespace:  namespace,
		outputDir:  outputDir,
		httpClient: &http.Client{Timeout: 30 * time.Second},
	}, nil
}

// CollectCoverageFromPod collects coverage data from a pod via port-forwarding
func (c *CoverageClient) CollectCoverageFromPod(ctx context.Context, podName, testName string, targetPort int) error {
	fmt.Printf("📊 Collecting coverage from pod %s for test: %s\n", podName, testName)

	// Setup port forwarding
	localPort, stopChan, err := c.setupPortForward(podName, targetPort)
	if err != nil {
		return fmt.Errorf("setup port forward: %w", err)
	}
	defer close(stopChan)

	// Wait a bit for port forward to be ready
	time.Sleep(2 * time.Second)

	// Collect coverage via HTTP
	coverageURL := fmt.Sprintf("http://localhost:%d/coverage", localPort)
	if err := c.collectCoverageFromURL(coverageURL, testName); err != nil {
		return fmt.Errorf("collect coverage: %w", err)
	}

	fmt.Printf("✅ Coverage collected successfully for test: %s\n", testName)
	return nil
}

// CollectCoverageFromURL collects coverage data from a direct URL (no port-forwarding)
func (c *CoverageClient) CollectCoverageFromURL(coverageURL, testName string) error {
	return c.collectCoverageFromURL(coverageURL, testName)
}

// setupPortForward sets up port forwarding to the pod
func (c *CoverageClient) setupPortForward(podName string, targetPort int) (int, chan struct{}, error) {
	// Use a local port (let the system choose)
	localPort := 0 // 0 means let the system choose

	path := fmt.Sprintf("/api/v1/namespaces/%s/pods/%s/portforward", c.namespace, podName)
	hostIP := strings.TrimPrefix(c.restConfig.Host, "https://")
	serverURL, err := url.Parse(fmt.Sprintf("https://%s%s", hostIP, path))
	if err != nil {
		return 0, nil, fmt.Errorf("parse server URL: %w", err)
	}

	transport, upgrader, err := spdy.RoundTripperFor(c.restConfig)
	if err != nil {
		return 0, nil, fmt.Errorf("create round tripper: %w", err)
	}

	dialer := spdy.NewDialer(upgrader, &http.Client{Transport: transport}, "POST", serverURL)

	stopChan := make(chan struct{}, 1)
	readyChan := make(chan struct{})

	// Create port forward
	ports := []string{fmt.Sprintf("%d:%d", localPort, targetPort)}

	out := io.Discard
	errOut := io.Discard

	forwarder, err := portforward.New(dialer, ports, stopChan, readyChan, out, errOut)
	if err != nil {
		return 0, nil, fmt.Errorf("create port forwarder: %w", err)
	}

	// Start port forwarding in background
	go func() {
		if err := forwarder.ForwardPorts(); err != nil {
			fmt.Printf("⚠️  Port forward error: %v\n", err)
		}
	}()

	// Wait for ready signal
	select {
	case <-readyChan:
		// Get the actual local port that was assigned
		forwardedPorts, err := forwarder.GetPorts()
		if err != nil || len(forwardedPorts) == 0 {
			close(stopChan)
			return 0, nil, fmt.Errorf("get forwarded ports: %w", err)
		}
		actualLocalPort := int(forwardedPorts[0].Local)
		fmt.Printf("✅ Port forward ready: localhost:%d -> pod:%d\n", actualLocalPort, targetPort)
		return actualLocalPort, stopChan, nil
	case <-time.After(30 * time.Second):
		close(stopChan)
		return 0, nil, fmt.Errorf("timeout waiting for port forward")
	}
}

// collectCoverageFromURL collects coverage from the given URL
func (c *CoverageClient) collectCoverageFromURL(coverageURL, testName string) error {
	// Prepare request body
	reqBody, err := json.Marshal(map[string]string{
		"test_name": testName,
	})
	if err != nil {
		return fmt.Errorf("marshal request: %w", err)
	}

	// Send POST request to coverage endpoint
	resp, err := c.httpClient.Post(coverageURL, "application/json", bytes.NewReader(reqBody))
	if err != nil {
		return fmt.Errorf("send coverage request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("coverage endpoint returned %d: %s", resp.StatusCode, body)
	}

	// Parse response
	var covResp CoverageResponse
	if err := json.NewDecoder(resp.Body).Decode(&covResp); err != nil {
		return fmt.Errorf("decode coverage response: %w", err)
	}

	// Decode and save metadata
	metaData, err := base64.StdEncoding.DecodeString(covResp.MetaData)
	if err != nil {
		return fmt.Errorf("decode metadata: %w", err)
	}

	// Decode and save counters
	counterData, err := base64.StdEncoding.DecodeString(covResp.CountersData)
	if err != nil {
		return fmt.Errorf("decode counters: %w", err)
	}

	// Create test-specific subdirectory
	testDir := filepath.Join(c.outputDir, testName)
	if err := os.MkdirAll(testDir, 0755); err != nil {
		return fmt.Errorf("create test directory: %w", err)
	}

	// Save files with proper names
	metaPath := filepath.Join(testDir, covResp.MetaFilename)
	if err := os.WriteFile(metaPath, metaData, 0644); err != nil {
		return fmt.Errorf("write metadata file: %w", err)
	}

	counterPath := filepath.Join(testDir, covResp.CountersFilename)
	if err := os.WriteFile(counterPath, counterData, 0644); err != nil {
		return fmt.Errorf("write counters file: %w", err)
	}

	fmt.Printf("  📁 Saved: %s\n", metaPath)
	fmt.Printf("  📁 Saved: %s\n", counterPath)

	return nil
}

// GenerateCoverageReport generates a text coverage report from collected data
func (c *CoverageClient) GenerateCoverageReport(testName string) error {
	testDir := filepath.Join(c.outputDir, testName)
	reportPath := filepath.Join(testDir, "coverage.txt")

	fmt.Printf("📊 Generating coverage report for test: %s\n", testName)

	// Run go tool covdata to convert binary format to text
	cmd := exec.Command("go", "tool", "covdata", "textfmt",
		"-i="+testDir,
		"-o="+reportPath)

	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("generate coverage report: %w\nOutput: %s", err, output)
	}

	fmt.Printf("✅ Coverage report generated: %s\n", reportPath)
	return nil
}

// FilterCoverageReport filters out coverage_server.go from the coverage report
func (c *CoverageClient) FilterCoverageReport(testName string) error {
	testDir := filepath.Join(c.outputDir, testName)
	reportPath := filepath.Join(testDir, "coverage.txt")
	filteredPath := filepath.Join(testDir, "coverage_filtered.txt")

	data, err := os.ReadFile(reportPath)
	if err != nil {
		return fmt.Errorf("read coverage report: %w", err)
	}

	lines := strings.Split(string(data), "\n")
	var filtered []string

	for _, line := range lines {
		// Skip lines that reference coverage_server.go
		if !strings.Contains(line, "coverage_server.go") {
			filtered = append(filtered, line)
		}
	}

	filteredData := strings.Join(filtered, "\n")
	if err := os.WriteFile(filteredPath, []byte(filteredData), 0644); err != nil {
		return fmt.Errorf("write filtered report: %w", err)
	}

	fmt.Printf("✅ Filtered coverage report: %s\n", filteredPath)
	return nil
}

// GenerateHTMLReport generates an HTML coverage report
func (c *CoverageClient) GenerateHTMLReport(testName string) error {
	testDir := filepath.Join(c.outputDir, testName)
	reportPath := filepath.Join(testDir, "coverage_filtered.txt")
	htmlPath := filepath.Join(testDir, "coverage.html")

	// Check if filtered report exists, fallback to regular report
	if _, err := os.Stat(reportPath); os.IsNotExist(err) {
		reportPath = filepath.Join(testDir, "coverage.txt")
	}

	fmt.Printf("📊 Generating HTML coverage report for test: %s\n", testName)

	cmd := exec.Command("go", "tool", "cover",
		"-html="+reportPath,
		"-o="+htmlPath)

	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("generate HTML report: %w\nOutput: %s", err, output)
	}

	fmt.Printf("✅ HTML report generated: %s\n", htmlPath)
	return nil
}

// PrintCoverageSummary prints a summary of the coverage data
func (c *CoverageClient) PrintCoverageSummary(testName string) error {
	testDir := filepath.Join(c.outputDir, testName)
	reportPath := filepath.Join(testDir, "coverage_filtered.txt")

	// Check if filtered report exists, fallback to regular report
	if _, err := os.Stat(reportPath); os.IsNotExist(err) {
		reportPath = filepath.Join(testDir, "coverage.txt")
	}

	data, err := os.ReadFile(reportPath)
	if err != nil {
		return fmt.Errorf("read coverage report: %w", err)
	}

	fmt.Printf("\n📊 Coverage Summary for test: %s\n", testName)
	fmt.Println(strings.Repeat("=", 60))
	fmt.Println(string(data))
	fmt.Println(strings.Repeat("=", 60))

	return nil
}
