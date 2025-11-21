package models

import "time"

type ExchangeRate struct {
	ID           uint      `gorm:"primarykey" json:"_id"`
	FromCurrency string    `json:"fromCurrency" bind:"required"`
	ToCurrency   string    `json:"toCurrency" bind:"required"`
	Rate         float64   `json:"rate" bind:"required"`
	Date         time.Time `json:"date"`
}
