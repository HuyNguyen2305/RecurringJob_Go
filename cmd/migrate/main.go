// Command migrate manages tenant schemas.
//
// Apply migrations/*.sql to a schema, creating it if needed (applied files are
// tracked per schema, so it is re-runnable):
//
//	go run ./cmd/migrate -schema public
//
// Drop a schema and everything in it (idempotent; "public" is refused):
//
//	go run ./cmd/migrate -drop manual_check
package main

import (
	"flag"
	"fmt"
	"log"

	"github.com/joho/godotenv"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"

	"recurringjob/internal/common/dbconfig"
	"recurringjob/internal/migrator"
)

func main() {
	_ = godotenv.Load()
	schema := flag.String("schema", "public", "tenant schema to migrate")
	dir := flag.String("dir", "migrations", "directory with .sql files")
	drop := flag.String("drop", "", "drop this schema (CASCADE) instead of migrating")
	flag.Parse()

	db, err := gorm.Open(postgres.Open(dbconfig.DatabaseURL()), &gorm.Config{})
	if err != nil {
		log.Fatalf("connect to database: %v", err)
	}

	if *drop != "" {
		if err := migrator.DropSchema(db, *drop); err != nil {
			log.Fatalf("drop: %v", err)
		}
		fmt.Printf("schema %s does not exist\n", *drop)
		return
	}
	res, err := migrator.Apply(db, *schema, *dir)
	if err != nil {
		log.Fatalf("migrate: %v", err)
	}
	for _, f := range res.Applied {
		fmt.Printf("apply %s\n", f)
	}
	for _, f := range res.Skipped {
		fmt.Printf("skip  %s (already applied)\n", f)
	}
}
