// Command copy_reference_data copies customers, locations and service types
// from a source schema (default "public") into a tenant schema, once. The
// source is only read; rows already present in the target are left alone, so
// it is re-runnable.
//
//	go run ./scripts/copy_reference_data -to go_service
package main

import (
	"flag"
	"fmt"
	"log"
	"regexp"

	"github.com/joho/godotenv"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"

	"recurringjob/internal/common/dbconfig"
)

var ident = regexp.MustCompile(`^[a-z_][a-z0-9_]{0,62}$`)

var tables = []struct{ name, cols string }{
	{"customers", "id, name, email, phone, created_at, updated_at"},
	{"service_types", "id, name, description, created_at, updated_at"},
	{"locations", "id, customer_id, address_line1, city, state, zip, created_at, updated_at"},
}

func main() {
	_ = godotenv.Load()
	from := flag.String("from", "public", "source schema (read only)")
	to := flag.String("to", "", "target schema, already migrated")
	flag.Parse()
	if !ident.MatchString(*from) || !ident.MatchString(*to) || *from == *to {
		log.Fatal("-from and -to must be different, plain lower-case schema names")
	}
	db, err := gorm.Open(postgres.Open(dbconfig.DatabaseURL()), &gorm.Config{})
	if err != nil {
		log.Fatalf("connect to database: %v", err)
	}
	err = db.Transaction(func(tx *gorm.DB) error {
		for _, t := range tables {
			res := tx.Exec(fmt.Sprintf(
				`INSERT INTO %[2]s.%[1]s (%[3]s) SELECT %[3]s FROM %[4]s.%[1]s ON CONFLICT (id) DO NOTHING`,
				t.name, *to, t.cols, *from))
			if res.Error != nil {
				return fmt.Errorf("%s: %w", t.name, res.Error)
			}
			fmt.Printf("%-14s copied %d row(s)\n", t.name, res.RowsAffected)
		}
		return nil
	})
	if err != nil {
		log.Fatalf("copy (rolled back): %v", err)
	}
}
