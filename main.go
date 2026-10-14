package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/pdfcpu/pdfcpu/pkg/api"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/model"
)

const (
	remarkableBaseURL = "http://10.11.99.1"
	manifestFile      = ".sync_manifest.json"
	maxRetries        = 3
	retryDelay        = 1 * time.Second
	notebookFileType  = "notebook"
	allFlag           = "--all"
)

// RMDOC first: it is a quick copy of the raw files, while PDF export renders on the tablet.
var syncFormats = []string{"rmdoc", "pdf"}

// The tablet renders PDFs before responding, so the timeout covers render plus transfer.
var httpClient = &http.Client{Timeout: 10 * time.Minute}

// Color codes for terminal output
const (
	colorReset  = "\033[0m"
	colorRed    = "\033[31m"
	colorGreen  = "\033[32m"
	colorYellow = "\033[33m"
	colorBlue   = "\033[34m"
	colorCyan   = "\033[36m"
	colorBold   = "\033[1m"
	colorDim    = "\033[2m"
)

type Document struct {
	ID             string `json:"ID"`
	VissibleName   string `json:"VissibleName"`
	ModifiedClient string `json:"ModifiedClient"`
	Type           string `json:"Type"`
	FileType       string `json:"fileType"`
	Bookmarked     bool   `json:"Bookmarked"`
	CurrentPage    int    `json:"CurrentPage"`
	Parent         string `json:"Parent"`
}

type ManifestEntry struct {
	// LastSync is the legacy single timestamp; loadManifest migrates it into PDF and RMDoc.
	LastSync string `json:"last_sync,omitempty"`
	PDF      string `json:"pdf,omitempty"`
	RMDoc    string `json:"rmdoc,omitempty"`
}

func (e ManifestEntry) synced(format string) string {
	if format == "pdf" {
		return e.PDF
	}
	return e.RMDoc
}

func (e *ManifestEntry) markSynced(format, modifiedClient string) {
	if format == "pdf" {
		e.PDF = modifiedClient
	} else {
		e.RMDoc = modifiedClient
	}
}

type SyncStats struct {
	Downloaded int
	Skipped    int
	Failed     int
}

func main() {
	fmt.Printf("%s%sreMarkable Sync Utility%s\n", colorBold, colorCyan, colorReset)
	fmt.Printf("%s━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━%s\n\n", colorDim, colorReset)

	var args []string
	includeAll := false
	for _, a := range os.Args[1:] {
		if a == allFlag {
			includeAll = true
		} else {
			args = append(args, a)
		}
	}

	if len(args) > 0 {
		arg := args[0]

		if arg == "list" {
			fmt.Printf("%s📡 Fetching documents list...%s\n\n", colorBlue, colorReset)
			documents, err := fetchDocuments(includeAll)
			if err != nil {
				fmt.Printf("%s✗ Failed to fetch documents: %v%s\n", colorRed, err, colorReset)
				os.Exit(1)
			}
			for _, doc := range documents {
				fmt.Printf("%s%s%s\n", colorBold, doc.VissibleName, colorReset)
				fmt.Printf("  %s%s%s\n\n", colorDim, doc.ID, colorReset)
			}
			return
		}

		// Treat arg as notebook name
		lastPageOnly := len(args) > 1 && args[1] == "last"

		fmt.Printf("%s📡 Fetching documents list...%s\n\n", colorBlue, colorReset)
		documents, err := fetchDocuments(includeAll)
		if err != nil {
			fmt.Printf("%s✗ Failed to fetch documents: %v%s\n", colorRed, err, colorReset)
			os.Exit(1)
		}

		var found *Document
		for i, doc := range documents {
			if strings.EqualFold(doc.VissibleName, arg) {
				found = &documents[i]
				break
			}
		}
		if found == nil {
			fmt.Printf("%s✗ No notebook found with name: %s%s\n", colorRed, arg, colorReset)
			os.Exit(1)
		}

		fmt.Printf("%s%s%s\n", colorBold, found.VissibleName, colorReset)

		if err := ensureDirectories(); err != nil {
			fmt.Printf("%s✗ Failed to create directories: %v%s\n", colorRed, err, colorReset)
			os.Exit(1)
		}

		if lastPageOnly {
			if err := downloadLastPage(found.ID, found.VissibleName); err != nil {
				fmt.Printf("%s✗ Failed to download last page: %v%s\n", colorRed, err, colorReset)
				os.Exit(1)
			}
		} else {
			downloadFile(found.ID, found.VissibleName, "pdf")
		}
		return
	}

	// Ensure directories exist
	if err := ensureDirectories(); err != nil {
		fmt.Printf("%s✗ Failed to create directories: %v%s\n", colorRed, err, colorReset)
		os.Exit(1)
	}

	// Load existing manifest
	manifest, err := loadManifest()
	if err != nil {
		fmt.Printf("%s✗ Failed to load manifest: %v%s\n", colorRed, err, colorReset)
		os.Exit(1)
	}

	// Fetch documents list
	fmt.Printf("%s📡 Fetching documents list...%s\n", colorBlue, colorReset)
	documents, err := fetchDocuments(includeAll)
	if err != nil {
		fmt.Printf("%s✗ Failed to fetch documents: %v%s\n", colorRed, err, colorReset)
		fmt.Printf("%s💡 Make sure your reMarkable tablet is connected and accessible at %s%s\n", colorYellow, remarkableBaseURL, colorReset)
		os.Exit(1)
	}

	kind := "notebooks"
	if includeAll {
		kind = "documents"
	}
	fmt.Printf("%s✓%s Found %d %s\n\n", colorGreen, colorReset, len(documents), kind)

	stats := make(map[string]SyncStats)
	for _, format := range syncFormats {
		fmt.Printf("%s%s%s pass%s\n\n", colorBold, colorCyan, strings.ToUpper(format), colorReset)
		stats[format] = syncFormat(manifest, documents, format)
	}

	// Print summary
	fmt.Printf("%s━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━%s\n", colorDim, colorReset)
	fmt.Printf("%s%sSummary%s\n", colorBold, colorCyan, colorReset)
	for _, format := range syncFormats {
		st := stats[format]
		fmt.Printf("  %s%s%s\n", colorBold, strings.ToUpper(format), colorReset)
		fmt.Printf("    %s✓ Downloaded:%s   %d\n", colorGreen, colorReset, st.Downloaded)
		fmt.Printf("    %s⊘ Skipped:%s      %d (up to date)\n", colorYellow, colorReset, st.Skipped)
		if st.Failed > 0 {
			fmt.Printf("    %s✗ Failed:%s       %d\n", colorRed, colorReset, st.Failed)
		}
	}
	fmt.Printf("%s━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━%s\n", colorDim, colorReset)
}

func syncFormat(manifest map[string]ManifestEntry, documents []Document, format string) SyncStats {
	stats := SyncStats{}
	for _, doc := range documents {
		entry := manifest[doc.ID]
		// The tablet trims trailing zeros from fractional seconds, so ordering by string is unreliable.
		if entry.synced(format) == doc.ModifiedClient {
			stats.Skipped++
			continue
		}

		fmt.Printf("%s%s%s\n", colorBold, doc.VissibleName, colorReset)
		fmt.Printf("  ID: %s%s%s\n", colorDim, doc.ID, colorReset)
		if !downloadFile(doc.ID, doc.VissibleName, format) {
			stats.Failed++
			fmt.Println()
			continue
		}

		stats.Downloaded++
		entry.markSynced(format, doc.ModifiedClient)
		manifest[doc.ID] = entry
		// Saved per document because a lost connection exits mid-sync.
		if err := saveManifest(manifest); err != nil {
			fmt.Printf("%s✗ Failed to save manifest: %v%s\n", colorRed, err, colorReset)
			os.Exit(1)
		}
		fmt.Println()
	}
	return stats
}

func ensureDirectories() error {
	if err := os.MkdirAll("downloads/pdf", 0755); err != nil {
		return err
	}
	if err := os.MkdirAll("downloads/rmdoc", 0755); err != nil {
		return err
	}
	return nil
}

func loadManifest() (map[string]ManifestEntry, error) {
	manifest := make(map[string]ManifestEntry)

	data, err := os.ReadFile(manifestFile)
	if err != nil {
		if os.IsNotExist(err) {
			return manifest, nil
		}
		return nil, err
	}

	if err := json.Unmarshal(data, &manifest); err != nil {
		return nil, err
	}

	for id, entry := range manifest {
		if entry.LastSync != "" {
			manifest[id] = ManifestEntry{PDF: entry.LastSync, RMDoc: entry.LastSync}
		}
	}
	return manifest, nil
}

func saveManifest(manifest map[string]ManifestEntry) error {
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return err
	}

	tmp := manifestFile + ".tmp"
	if err := os.WriteFile(tmp, data, 0644); err != nil {
		return err
	}
	return os.Rename(tmp, manifestFile)
}

func fetchDocuments(includeAll bool) ([]Document, error) {
	resp, err := httpClient.Get(remarkableBaseURL + "/documents/")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP request failed with status: %d", resp.StatusCode)
	}

	var documents []Document
	if err := json.NewDecoder(resp.Body).Decode(&documents); err != nil {
		return nil, err
	}

	if includeAll {
		return documents, nil
	}
	notebooks := documents[:0]
	for _, doc := range documents {
		if doc.FileType == notebookFileType {
			notebooks = append(notebooks, doc)
		}
	}
	return notebooks, nil
}

func downloadFile(id, name, format string) bool {
	for retry := 0; retry < maxRetries; retry++ {
		if retry > 0 {
			fmt.Printf("  %s↻ Retry %d/%d...%s\n", colorYellow, retry, maxRetries-1, colorReset)
			time.Sleep(retryDelay)

			// Check if we can still reach the tablet before retrying
			if !checkConnectivity() {
				fmt.Printf("  %s✗ Cannot reach reMarkable tablet at %s%s\n", colorRed, remarkableBaseURL, colorReset)
				fmt.Printf("  %s💡 Please check your connection and try again%s\n", colorYellow, colorReset)
				os.Exit(1)
			}
		}

		if downloadFileOnce(id, name, format) {
			fmt.Printf("  %s✓ Downloaded %s%s\n", colorGreen, format, colorReset)
			return true
		}

		if retry == maxRetries-1 {
			fmt.Printf("  %s✗ Failed to download %s%s\n", colorRed, format, colorReset)
			return false
		}
	}

	return false
}

func downloadFileOnce(id, name, format string) bool {
	url := fmt.Sprintf("%s/download/%s/%s", remarkableBaseURL, id, format)

	resp, err := httpClient.Get(url)
	if err != nil {
		return false
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return false
	}

	// Sanitize filename
	safeName := sanitizeFilename(name)
	filename := filepath.Join("downloads", format, fmt.Sprintf("%s-%s.%s", safeName, id, format))

	partial := filename + ".part"
	file, err := os.Create(partial)
	if err != nil {
		return false
	}

	_, copyErr := io.Copy(file, resp.Body)
	closeErr := file.Close()
	if copyErr != nil || closeErr != nil {
		os.Remove(partial)
		return false
	}
	return os.Rename(partial, filename) == nil
}

func checkConnectivity() bool {
	client := &http.Client{
		Timeout: 3 * time.Second,
	}

	resp, err := client.Get(remarkableBaseURL + "/documents/")
	if err != nil {
		return false
	}
	defer resp.Body.Close()

	return resp.StatusCode == http.StatusOK
}

func downloadLastPage(id, name string) error {
	url := fmt.Sprintf("%s/download/%s/pdf", remarkableBaseURL, id)
	resp, err := httpClient.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	pdfBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}

	// Get page count
	pageCount, err := api.PageCount(bytes.NewReader(pdfBytes), model.NewDefaultConfiguration())
	if err != nil {
		return fmt.Errorf("reading pdf: %w", err)
	}

	// Extract last page — output written to downloads/pdf/{safeName}_page_{pageCount}.pdf
	safeName := sanitizeFilename(name)
	outDir := filepath.Join("downloads", "pdf")
	pageRange := fmt.Sprintf("%d", pageCount)
	if err := api.ExtractPages(bytes.NewReader(pdfBytes), outDir, safeName, []string{pageRange}, model.NewDefaultConfiguration()); err != nil {
		return fmt.Errorf("extracting last page: %w", err)
	}

	filename := filepath.Join(outDir, fmt.Sprintf("%s_page_%d.pdf", safeName, pageCount))
	fmt.Printf("  %s✓ Downloaded last page (page %d) → %s%s\n", colorGreen, pageCount, filename, colorReset)
	return nil
}

func sanitizeFilename(name string) string {
	var result strings.Builder
	for _, c := range name {
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') ||
			c == '-' || c == '_' || c == ' ' || c == '.' {
			result.WriteRune(c)
		} else {
			result.WriteRune('_')
		}
	}
	return result.String()
}
