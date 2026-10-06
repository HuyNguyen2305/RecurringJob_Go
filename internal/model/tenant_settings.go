package model

import "time"

// TenantSettings is the single row (id = 1) of the tenant_settings table.
type TenantSettings struct {
	ID        int16  `gorm:"primaryKey;autoIncrement:false"`
	Timezone  string `gorm:"not null;default:UTC"` // IANA name, e.g. America/Los_Angeles
	CreatedAt time.Time
	UpdatedAt time.Time
}
