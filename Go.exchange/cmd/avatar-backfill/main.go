package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	"Go.exchange/avatarbackfill"
	"Go.exchange/config"
	"Go.exchange/global"
)

func main() {
	if err := run(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "ERROR:", err)
		os.Exit(1)
	}
}

func run(args []string, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("avatar-backfill", flag.ContinueOnError)
	flags.SetOutput(stderr)
	var apply bool
	batchSize := avatarbackfill.DefaultBatchSize
	flags.BoolVar(&apply, "apply", false, "write V1 objects and update database metadata")
	flags.IntVar(&batchSize, "batch-size", batchSize, "users/accounts per keyset batch")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("unexpected positional arguments")
	}
	if batchSize < 1 {
		return errors.New("--batch-size must be at least 1")
	}

	config.InitDatabaseConfig()
	if global.Db == nil {
		return errors.New("database is not initialized")
	}
	store, err := avatarbackfill.NewConfiguredMinioObjectStore()
	if err != nil {
		return err
	}
	report, err := avatarbackfill.Run(context.Background(), global.Db, store, avatarbackfill.Options{Apply: apply, BatchSize: batchSize})
	if err != nil {
		return err
	}
	report.WriteReport(stdout)
	if apply && report.Failed > 0 {
		return fmt.Errorf("avatar backfill completed with %d failures", report.Failed)
	}
	return nil
}
