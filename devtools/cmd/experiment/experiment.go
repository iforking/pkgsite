// Copyright 2020 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.
// The experiments command can be used for modifying experiment state in the
// database.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"

	"golang.org/x/pkgsite/internal"
	"golang.org/x/pkgsite/internal/config"
	"golang.org/x/pkgsite/internal/database"
	"golang.org/x/pkgsite/internal/postgres"
)

const usage = `
List experiments:
    experiments [flags...] list

Create a new experiment:
    experiments [flags...] create <name> <description>

Update an experiment:
    experiments [flags...] update <name> <description>
`

var rollout = flag.Uint("rollout", 100, "experiment rollout percentage")

func exitUsage() {
	flag.Usage()
	os.Exit(2)
}
func main() {
	flag.Usage = func() {
		fmt.Fprint(flag.CommandLine.Output(), usage)
		fmt.Fprintln(flag.CommandLine.Output())
		fmt.Fprintln(flag.CommandLine.Output(), "Flags:")
		flag.PrintDefaults()
	}
	flag.Parse()
	if flag.NArg() < 1 {
		exitUsage()
	}
	ctx := context.Background()
	cfg, err := config.Init(ctx)
	if err != nil {
		log.Fatal(ctx, err)
	}
	cfg.Dump(os.Stderr)
	ddb, err := database.Open("postgres", cfg.DBConnInfo(), cfg.InstanceID)
	if err != nil {
		log.Fatal(ctx, err)
	}
	defer ddb.Close()
	db := postgres.New(ddb)
	switch flag.Arg(0) {
	case "ls":
		if err := listExperiments(ctx, db); err != nil {
			log.Fatalf("listing experiments: %v", err)
		}
	case "create":
		if flag.NArg() < 2 {
			fmt.Println(flag.NArg())
			exitUsage()
		}
		if err := createExperiment(ctx, db, flag.Arg(1), flag.Arg(2), *rollout); err != nil {
			log.Fatalf("creating experiment: %v", err)
		}
	case "update":
		if flag.NArg() < 2 {
			exitUsage()
		}
		if err := updateExperiment(ctx, db, flag.Arg(1), flag.Arg(2), *rollout); err != nil {
			log.Fatalf("updating experiment: %v", err)
		}
	default:
		exitUsage()
	}
}
func listExperiments(ctx context.Context, db *postgres.DB) error {
	exps, err := db.GetExperiments(ctx)
	if err != nil {
		return err
	}
	fmt.Printf("%30s %12s %-40s\n", "NAME", "ROLLOUT", "DESCRIPTION")
	for _, exp := range exps {
		fmt.Printf("%30s %12d %-40s\n", exp.Name, exp.Rollout, exp.Description)
	}
	return nil
}

func createExperiment(ctx context.Context, db *postgres.DB, name, description string, rollout uint) error {
	exp := &internal.Experiment{
		Name:        name,
		Description: description,
		Rollout:     rollout,
	}
	if err := db.InsertExperiment(ctx, exp); err != nil {
		return err
	}
	fmt.Printf("\nCreated experiment %q with rollout=%d.\n", name, rollout)
	return nil
}

func updateExperiment(ctx context.Context, db *postgres.DB, name, description string, rollout uint) error {
	exp := &internal.Experiment{
		Name:        name,
		Description: description,
		Rollout:     rollout,
	}
	if err := db.UpdateExperiment(ctx, exp); err != nil {
		return err
	}
	fmt.Printf("\nUpdated experiment %q; rollout=%d.\n", name, rollout)
	return nil
}