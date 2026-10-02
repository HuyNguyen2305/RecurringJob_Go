// Command api runs the Job Recurring HTTP server.
package main

import (
	"log"

	"github.com/joho/godotenv"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"

	"recurringjob/internal/app"
	"recurringjob/internal/common/dbconfig"
)

func main() {
	_ = godotenv.Load() // optional .env; real environment variables win

	db, err := gorm.Open(postgres.Open(dbconfig.DatabaseURL()), &gorm.Config{})
	if err != nil {
		log.Fatalf("connect to database: %v", err)
	}

	r := app.NewServer(db, dbconfig.Env("DEFAULT_TENANT_SCHEMA", "public"))

	addr := ":" + dbconfig.Env("PORT", "8080")
	log.Printf("listening on %s", addr)
	if err := r.Run(addr); err != nil {
		log.Fatal(err)
	}
}
