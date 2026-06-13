package main

import (
	"database/sql"
	"errors"
)

var ErrInsufficientFunds = errors.New("insufficient funds")

type Repository struct {
	db *sql.DB
}

func NewRepository(db *sql.DB) *Repository {
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	return &Repository{db: db}
}

func (r *Repository) CreateWallet(id string, balance int64) error {
	_, err := r.db.Exec(`INSERT INTO wallets (id, balance) VALUES (?, ?)`, id, balance)
	return err
}

func (r *Repository) GetWalletBalance(id string) (int64, error) {
	var b int64
	err := r.db.QueryRow(`SELECT balance FROM wallets WHERE id = ?`, id).Scan(&b)
	if err == sql.ErrNoRows {
		return 0, nil
	}
	return b, err
}

// conditional debit: subtract amount if balance >= amount
func (r *Repository) DebitIfEnough(tx *sql.Tx, walletID string, amount int64) error {
	res, err := tx.Exec(`UPDATE wallets SET balance = balance - ? WHERE id = ? AND balance >= ?`, amount, walletID, amount)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrInsufficientFunds
	}
	return nil
}

func (r *Repository) Credit(tx *sql.Tx, walletID string, amount int64) error {
	_, err := tx.Exec(`UPDATE wallets SET balance = balance + ? WHERE id = ?`, amount, walletID)
	return err
}

func (r *Repository) InsertTransfer(tx *sql.Tx, t Transfer) error {
	_, err := tx.Exec(`INSERT INTO transfers (id, from_wallet_id, to_wallet_id, amount, state) VALUES (?, ?, ?, ?, ?)`,
		t.ID, t.FromWalletID, t.ToWalletID, t.Amount, t.State)
	return err
}

func (r *Repository) UpdateTransferState(tx *sql.Tx, transferID, state string) error {
	_, err := tx.Exec(`UPDATE transfers SET state = ? WHERE id = ?`, state, transferID)
	return err
}

func (r *Repository) InsertLedgerEntry(tx *sql.Tx, e LedgerEntry) error {
	_, err := tx.Exec(`INSERT INTO ledger_entries (wallet_id, transfer_id, type, amount) VALUES (?, ?, ?, ?)`,
		e.WalletID, e.TransferID, e.Type, e.Amount)
	return err
}

func (r *Repository) InsertIdempotency(tx *sql.Tx, key string, status string) error {
	_, err := tx.Exec(`INSERT INTO idempotency_records (idempotency_key, status) VALUES (?, ?)`, key, status)
	return err
}

func (r *Repository) GetIdempotency(key string) (string, string, error) {
	var transferID sql.NullString
	var status string
	err := r.db.QueryRow(`SELECT transfer_id, status FROM idempotency_records WHERE idempotency_key = ?`, key).Scan(&transferID, &status)
	if err != nil {
		return "", "", err
	}
	return transferID.String, status, nil
}

func (r *Repository) CompleteIdempotency(tx *sql.Tx, key, transferID string) error {
	_, err := tx.Exec(`UPDATE idempotency_records SET transfer_id = ?, status = ? WHERE idempotency_key = ?`, transferID, "COMPLETED", key)
	return err
}
