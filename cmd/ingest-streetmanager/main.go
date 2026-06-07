// Command ingest-streetmanager streams one monthly DfT Street Manager archive,
// filters it to London highway authorities, and reports the planned-works
// picture: volume, categories, traffic management, and how far ahead works are
// filed (the forward signal the burden forecast needs).
//
// It streams straight from the public S3 archive without storing the ~1 GB zip,
// and (with -write) emits only a compact London extract. None of the Street
// Manager data is committed to the repo; the source is cited instead. See
// internal/streetmanager for the bucket URL and OGL attribution.
package main

import (
	"compress/gzip"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/umbralcalc/traffic-forecaster/internal/streetmanager"
)

var errLimit = errors.New("entry limit reached")

func main() {
	recType := flag.String("type", "permit", "archive record type: permit | activity | section_58")
	month := flag.String("month", "", "month to ingest, YYYY-MM (e.g. 2026-05)")
	file := flag.String("file", "", "read a local archive file instead of downloading (for testing)")
	outDir := flag.String("out", filepath.Join("data", "streetmanager"), "directory for the London extract (gitignored)")
	write := flag.Bool("write", false, "write the filtered London extract as gzipped NDJSON")
	limit := flag.Int("limit", 0, "stop after N total entries (0 = all; for quick tests)")
	timeout := flag.Duration("timeout", 30*time.Minute, "overall deadline")
	flag.Parse()

	if err := run(*recType, *month, *file, *outDir, *write, *limit, *timeout); err != nil {
		fmt.Fprintln(os.Stderr, "ingest-streetmanager:", err)
		os.Exit(1)
	}
}

type stats struct {
	total, london           int
	authorities             map[string]int
	eventTypes              map[string]int
	workCategories          map[string]int
	trafficManagement       map[string]int
	proposedFuture          int    // London works proposed to start after today
	maxProposedStart        string // furthest-ahead proposed start seen
	proposedStartsByYearMon map[string]int
}

func run(recType, month, file, outDir string, write bool, limit int, timeout time.Duration) error {
	src, label, err := openSource(recType, month, file, timeout)
	if err != nil {
		return err
	}
	defer src.Close()

	today := time.Now().UTC().Format("2006-01-02")
	st := &stats{
		authorities: map[string]int{}, eventTypes: map[string]int{},
		workCategories: map[string]int{}, trafficManagement: map[string]int{},
		proposedStartsByYearMon: map[string]int{},
	}

	var gzw *gzip.Writer
	var outFile *os.File
	if write {
		if err := os.MkdirAll(outDir, 0o755); err != nil {
			return err
		}
		name := fmt.Sprintf("%s-%s.london.ndjson.gz", recType, strings.ReplaceAll(month, "-", "_"))
		outFile, err = os.Create(filepath.Join(outDir, name))
		if err != nil {
			return err
		}
		defer outFile.Close()
		gzw = gzip.NewWriter(outFile)
		defer gzw.Close()
	}

	fmt.Printf("streaming %s ...\n", label)
	streamErr := streetmanager.Stream(src, func(r streetmanager.Record) error {
		st.total++
		if limit > 0 && st.total > limit {
			return errLimit
		}
		ha := r.HighwayAuthority()
		if !streetmanager.IsLondon(ha) {
			return nil
		}
		st.london++
		st.authorities[ha]++
		st.eventTypes[bucket(r.EventType)]++
		if c := r.Field("work_category"); c != "" {
			st.workCategories[c]++
		}
		if tm := firstNonEmpty(r.Field("current_traffic_management_type"), r.Field("traffic_management_type")); tm != "" {
			st.trafficManagement[tm]++
		}
		if ps := r.Field("proposed_start_date"); ps != "" {
			day := dateOnly(ps)
			if day > today {
				st.proposedFuture++
			}
			if day > st.maxProposedStart {
				st.maxProposedStart = day
			}
			if len(day) >= 7 {
				st.proposedStartsByYearMon[day[:7]]++
			}
		}
		if gzw != nil {
			line, _ := r.MarshalLine()
			gzw.Write(line)
			gzw.Write([]byte("\n"))
		}
		return nil
	})
	if streamErr != nil && !errors.Is(streamErr, errLimit) {
		return streamErr
	}

	report(st, recType, today, write, outDir)
	return nil
}

func openSource(recType, month, file string, timeout time.Duration) (readCloser, string, error) {
	if file != "" {
		f, err := os.Open(file)
		return f, "local file " + file, err
	}
	y, m, err := parseMonth(month)
	if err != nil {
		return nil, "", err
	}
	url := streetmanager.ObjectURL(recType, y, m)
	client := &http.Client{Timeout: timeout}
	req, _ := http.NewRequest(http.MethodGet, url, nil)
	req.Header.Set("User-Agent", "traffic-forecaster (+https://github.com/umbralcalc/traffic-forecaster)")
	resp, err := client.Do(req)
	if err != nil {
		return nil, url, err
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, url, fmt.Errorf("GET %s: status %d", url, resp.StatusCode)
	}
	return resp.Body, url, nil
}

func report(st *stats, recType, today string, wrote bool, outDir string) {
	fmt.Printf("\n== Street Manager %s — London ==\n", recType)
	fmt.Printf("total entries: %d   London entries: %d (%.1f%%)\n",
		st.total, st.london, pct(st.london, st.total))
	fmt.Printf("distinct London authorities: %d\n", len(st.authorities))

	fmt.Println("\nevent types:")
	for _, kv := range top(st.eventTypes, 12) {
		fmt.Printf("  %-28s %6d\n", kv.k, kv.n)
	}
	if len(st.workCategories) > 0 {
		fmt.Println("\nwork categories:")
		for _, kv := range top(st.workCategories, 12) {
			fmt.Printf("  %-28s %6d\n", kv.k, kv.n)
		}
	}
	if len(st.trafficManagement) > 0 {
		fmt.Println("\ntraffic management (burden weight signal):")
		for _, kv := range top(st.trafficManagement, 12) {
			fmt.Printf("  %-28s %6d\n", kv.k, kv.n)
		}
	}
	fmt.Println("\nforward visibility (the whole point):")
	fmt.Printf("  London entries with proposed_start_date AFTER today (%s): %d\n", today, st.proposedFuture)
	fmt.Printf("  furthest-ahead proposed start: %s\n", orNone(st.maxProposedStart))
	if len(st.proposedStartsByYearMon) > 0 {
		fmt.Println("  proposed starts by month (top):")
		for _, kv := range top(st.proposedStartsByYearMon, 8) {
			fmt.Printf("    %-10s %6d\n", kv.k, kv.n)
		}
	}
	fmt.Printf("\n%s\n", streetmanager.Attribution)
	if wrote {
		fmt.Printf("London extract written under %s/ (gitignored)\n", outDir)
	}
}

// --- helpers ---

type readCloser interface {
	Read([]byte) (int, error)
	Close() error
}

type kv struct {
	k string
	n int
}

func top(m map[string]int, n int) []kv {
	out := make([]kv, 0, len(m))
	for k, v := range m {
		out = append(out, kv{k, v})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].n != out[j].n {
			return out[i].n > out[j].n
		}
		return out[i].k < out[j].k
	})
	if len(out) > n {
		out = out[:n]
	}
	return out
}

func parseMonth(month string) (int, int, error) {
	parts := strings.SplitN(month, "-", 2)
	if len(parts) != 2 {
		return 0, 0, fmt.Errorf("month must be YYYY-MM, got %q", month)
	}
	y, err1 := strconv.Atoi(parts[0])
	m, err2 := strconv.Atoi(parts[1])
	if err1 != nil || err2 != nil || m < 1 || m > 12 {
		return 0, 0, fmt.Errorf("invalid month %q", month)
	}
	return y, m, nil
}

func bucket(eventType string) string {
	if eventType == "" {
		return "(none)"
	}
	return eventType
}

func dateOnly(s string) string {
	if len(s) >= 10 {
		return s[:10]
	}
	return s
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

func orNone(s string) string {
	if s == "" {
		return "(none)"
	}
	return s
}

func pct(n, total int) float64 {
	if total == 0 {
		return 0
	}
	return 100 * float64(n) / float64(total)
}
