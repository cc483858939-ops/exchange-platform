package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log"
	"strings"

	"Go.exchange/config"
	"Go.exchange/embeddingstate"
	"Go.exchange/global"

	"gorm.io/gorm"
)

func updateServingVersion(ctx context.Context, db *gorm.DB, version string, output io.Writer) error {
	version = strings.TrimSpace(version)
	if version == "" {
		return fmt.Errorf("--version must not be blank")
	}
	if output == nil {
		return fmt.Errorf("output writer is nil")
	}
	oldVersion, err := embeddingstate.LoadServingVersion(ctx, db)
	if err != nil {
		return err
	}
	if err := embeddingstate.SetServingVersion(ctx, db, version); err != nil {
		return err
	}
	newVersion, err := embeddingstate.LoadServingVersion(ctx, db)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(output, "embedding serving version updated:\nold=%s\nnew=%s\n", oldVersion, newVersion)
	return err
}

func main() {
	version := flag.String("version", "", "embedding version selected for recommendation serving")
	flag.Parse()
	if strings.TrimSpace(*version) == "" {
		log.Fatal("--version must not be blank")
	}
	config.InitDatabaseConfig()
	if global.Db == nil {
		log.Fatal("database is not initialized")
	}
	if err := updateServingVersion(context.Background(), global.Db, *version, log.Writer()); err != nil {
		log.Fatal(err)
	}
}
