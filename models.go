package main

import "time"

type Wallet struct {
	ID        string `db:"id"`
	Balance   int64  `db:"balance"`
	CreatedAt time.Time
}

type Transfer struct {
	ID           string
	FromWalletID string
	ToWalletID   string
	Amount       int64
	State        string
	CreatedAt    time.Time
}

type LedgerEntry struct {
	ID         int64
	WalletID   string
	TransferID string
	Type       string
	Amount     int64
	CreatedAt  time.Time
}

type IdempotencyRecord struct {
	Key        string
	TransferID sqlNullString
	Status     string
	Response   sqlNullString
	CreatedAt  time.Time
}

type sqlNullString struct {
	Valid bool
	Str   string
}
