// Command pull-disruptions takes one immutable daily snapshot of the TfL road
// disruption feed and writes it to data/raw/YYYY-MM-DD.json.gz. It is designed
// to run as a scheduled GitHub Action (see .github/workflows/daily-snapshot.yml),
// not as a long-running service.
//
// The feed exposes only currently-active disruptions and carries no creation
// timestamp, so the only way to reconstruct each record's lifecycle is to bank
// these snapshots day after day. Snapshots are therefore append-only and never
// rewritten: a missed day is unrecoverable, which the void rule (see PLAN.md)
// accounts for honestly rather than hiding.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/umbralcalc/traffic-forecaster/internal/store"
	"github.com/umbralcalc/traffic-forecaster/internal/tfl"
)

func main() {
	outDir := flag.String("out", filepath.Join("data", "raw"), "directory for daily snapshot files")
	force := flag.Bool("force", false, "overwrite an existing snapshot for today (default: refuse, to keep snapshots immutable)")
	timeout := flag.Duration("timeout", 2*time.Minute, "overall deadline for the pull")
	flag.Parse()

	if err := run(*outDir, *force, *timeout); err != nil {
		fmt.Fprintln(os.Stderr, "pull-disruptions:", err)
		os.Exit(1)
	}
}

func run(outDir string, force bool, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	appKey := os.Getenv("TFL_APP_KEY")
	// captured_at is the source of truth for the filename too, so the date a
	// snapshot claims always matches the instant it was taken.
	captured := time.Now().UTC()
	path := store.Path(outDir, captured)

	if !force {
		if _, err := os.Stat(path); err == nil {
			fmt.Printf("snapshot %s already exists; skipping (use -force to overwrite)\n", path)
			return nil
		}
	}

	// Seed jitter from the capture time; exact reproducibility is not needed here.
	client := tfl.NewClient(appKey, captured.UnixNano())
	records, err := client.Disruptions(ctx)
	if err != nil {
		return err
	}

	snap := store.Snapshot{
		CapturedAt:  captured,
		Source:      tfl.DefaultBaseURL + "/Road/all/Disruption?stripContent=true",
		Count:       len(records),
		AppKeyUsed:  appKey != "",
		Disruptions: records,
	}

	written, err := store.Write(path, snap, force)
	if err != nil {
		return err
	}
	if !written {
		fmt.Printf("snapshot %s already exists; skipping (use -force to overwrite)\n", path)
		return nil
	}

	fmt.Printf("wrote %s (%d disruptions, app_key=%t)\n", path, snap.Count, snap.AppKeyUsed)
	return nil
}
