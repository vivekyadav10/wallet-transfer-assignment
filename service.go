package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math/rand"
	"time"
)

type Service struct {
	repo *Repository
}

func NewService(repo *Repository) *Service { return &Service{repo: repo} }

func init() {
	rand.Seed(time.Now().UnixNano())
}

type TransferRequest struct {
	IdempotencyKey string
	FromWalletID   string
	ToWalletID     string
	Amount         int64
}

type TransferResult struct {
	TransferID string `json:"transferId"`
	State      string `json:"state"`
}

func generateID() string {
	return fmt.Sprintf("t_%d_%d", time.Now().UnixNano(), rand.Intn(100000))
}

// Transfer implements a transactional, idempotent wallet transfer.
func (s *Service) Transfer(ctx context.Context, req TransferRequest) (TransferResult, error) {
	// optimistic claim via idempotency record
	// try to insert idempotency record; if exists and completed return existing transfer
	// if exists and in-progress, poll for completion

	// Try quick path: check if completed
	if req.IdempotencyKey != "" {
		tid, status, err := s.repo.GetIdempotency(ctx, req.IdempotencyKey)
		if err == nil && status == "COMPLETED" {
			return TransferResult{TransferID: tid, State: "PROCESSED"}, nil
		}
	}

	// insert a claim; use a DB transaction so that competing clients will fail the insert
	tx, err := s.repo.db.BeginTx(ctx, &sql.TxOptions{})
	if err != nil {
		return TransferResult{}, err
	}
	defer tx.Rollback()

	if req.IdempotencyKey != "" {
		if err := s.repo.InsertIdempotency(ctx, tx, req.IdempotencyKey, "IN_PROGRESS"); err != nil {
			// insertion failed: someone else claimed it. release tx and poll for completion with timeout
			tx.Rollback()
			wait := time.Millisecond * 50
			pollCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
			defer cancel()
			for {
				select {
				case <-pollCtx.Done():
					return TransferResult{}, errors.New("idempotency key in progress (timeout)")
				case <-time.After(wait):
					tid, status, err := s.repo.GetIdempotency(ctx, req.IdempotencyKey)
					if err == nil && status == "COMPLETED" {
						return TransferResult{TransferID: tid, State: "PROCESSED"}, nil
					}
				}
			}
		}
	}

	transferID := generateID()
	t := Transfer{ID: transferID, FromWalletID: req.FromWalletID, ToWalletID: req.ToWalletID, Amount: req.Amount, State: "PENDING"}
	if err := s.repo.InsertTransfer(ctx, tx, t); err != nil {
		return TransferResult{}, err
	}

	// try to debit
	if err := s.repo.DebitIfEnough(ctx, tx, req.FromWalletID, req.Amount); err != nil {
		if uerr := s.repo.UpdateTransferState(ctx, tx, transferID, "FAILED"); uerr != nil {
			tx.Rollback()
			return TransferResult{}, uerr
		}
		if cerr := tx.Commit(); cerr != nil {
			return TransferResult{}, cerr
		}
		return TransferResult{}, ErrInsufficientFunds
	}

	if err := s.repo.Credit(ctx, tx, req.ToWalletID, req.Amount); err != nil {
		if uerr := s.repo.UpdateTransferState(ctx, tx, transferID, "FAILED"); uerr != nil {
			tx.Rollback()
			return TransferResult{}, uerr
		}
		if cerr := tx.Commit(); cerr != nil {
			return TransferResult{}, cerr
		}
		return TransferResult{}, err
	}

	// ledger entries
	debit := LedgerEntry{WalletID: req.FromWalletID, TransferID: transferID, Type: "DEBIT", Amount: req.Amount}
	credit := LedgerEntry{WalletID: req.ToWalletID, TransferID: transferID, Type: "CREDIT", Amount: req.Amount}
	if err := s.repo.InsertLedgerEntry(ctx, tx, debit); err != nil {
		if uerr := s.repo.UpdateTransferState(ctx, tx, transferID, "FAILED"); uerr != nil {
			tx.Rollback()
			return TransferResult{}, uerr
		}
		if cerr := tx.Commit(); cerr != nil {
			return TransferResult{}, cerr
		}
		return TransferResult{}, err
	}
	if err := s.repo.InsertLedgerEntry(ctx, tx, credit); err != nil {
		if uerr := s.repo.UpdateTransferState(ctx, tx, transferID, "FAILED"); uerr != nil {
			tx.Rollback()
			return TransferResult{}, uerr
		}
		if cerr := tx.Commit(); cerr != nil {
			return TransferResult{}, cerr
		}
		return TransferResult{}, err
	}

	if err := s.repo.UpdateTransferState(ctx, tx, transferID, "PROCESSED"); err != nil {
		tx.Rollback()
		return TransferResult{}, err
	}

	if req.IdempotencyKey != "" {
		if err := s.repo.CompleteIdempotency(ctx, tx, req.IdempotencyKey, transferID); err != nil {
			tx.Rollback()
			return TransferResult{}, err
		}
	}

	if err := tx.Commit(); err != nil {
		return TransferResult{}, err
	}

	return TransferResult{TransferID: transferID, State: "PROCESSED"}, nil
}
