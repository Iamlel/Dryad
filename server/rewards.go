package main

// Caretaker rewards in test SOL on Solana devnet (no real money). The
// caretaker's wallet address comes in through POST /api/caretaker, and each
// time they water the plant, moving it from thirsty back into its healthy
// range, the server pays them from its own treasury wallet.

import (
	"context"
	"errors"
	"log"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/gagliardetto/solana-go"
	"github.com/gagliardetto/solana-go/programs/system"
	"github.com/gagliardetto/solana-go/rpc"
	"github.com/gagliardetto/solana-go/rpc/jsonrpc"
)

const (
	// rewardLamports is paid per watering: 0.01 SOL. A wallet that has never
	// held SOL can't receive less than 890,880 lamports (Solana's rent minimum).
	rewardLamports = solana.LAMPORTS_PER_SOL / 100
	// rewardCooldown: a watering this soon after a reward earns nothing, so
	// pulling the probe out and pushing it back in can't be farmed.
	rewardCooldown = time.Minute
	// wateredMarginPct: moisture has to rise this far above the plant's
	// minimum to count, so a reading wobbling around the minimum doesn't.
	wateredMarginPct = 5
)

var (
	errRewardsOff = errors.New("rewards are off: set SOLANA_TREASURY_KEY in server/.env")
	errBadWallet  = errors.New("that isn't a Solana wallet address")
)

// Payout is one reward sent.
type Payout struct {
	At          time.Time `json:"at"`
	Wallet      string    `json:"wallet"`
	SOL         float64   `json:"sol"`
	Signature   string    `json:"signature"`
	ExplorerURL string    `json:"explorer_url"`
}

// caretakerState is what GET /api/caretaker returns.
type caretakerState struct {
	Wallet  string   `json:"wallet"`   // "" until a wallet is set
	PlantID string   `json:"plant_id"` // "" means the default thresholds
	Payouts []Payout `json:"payouts"`  // newest first
}

// Rewards pays the caretaker for watering. Safe for concurrent use.
type Rewards struct {
	treasury solana.PrivateKey // nil when SOLANA_TREASURY_KEY isn't set
	rpc      *rpc.Client

	mu       sync.Mutex
	wallet   solana.PublicKey // zero until a wallet is set
	plant    Plant            // its thresholds judge the watering
	thirsty  bool             // moisture went below the minimum and hasn't been rewarded yet
	lastPaid time.Time
	payouts  []Payout
}

// newRewards pays from the treasury wallet whose base58 private key is
// treasuryKey (the format Phantom exports). It only ever talks to devnet,
// whose SOL is free test money, so no real money can move.
func newRewards(treasuryKey string) (*Rewards, error) {
	rw := &Rewards{plant: defaultPlant}
	if treasuryKey == "" {
		log.Print("rewards: SOLANA_TREASURY_KEY isn't set, so rewards are off")
		return rw, nil
	}
	key, err := solana.PrivateKeyFromBase58(treasuryKey)
	if err != nil {
		return nil, err
	}
	rw.treasury, rw.rpc = key, rpc.New(rpc.DevNet_RPC)
	log.Printf("rewards: paying test SOL on devnet from treasury %s", key.PublicKey())
	return rw, nil
}

func (rw *Rewards) On() bool { return rw.treasury != nil }

// SetCaretaker makes wallet the one paid for watering p.
func (rw *Rewards) SetCaretaker(wallet string, p Plant) error {
	key, err := solana.PublicKeyFromBase58(strings.TrimSpace(wallet))
	if err != nil {
		return errBadWallet
	}
	rw.mu.Lock()
	defer rw.mu.Unlock()
	rw.wallet, rw.plant, rw.thirsty = key, p, false
	log.Printf("rewards: caretaker is now %s", key)
	return nil
}

func (rw *Rewards) State() caretakerState {
	rw.mu.Lock()
	defer rw.mu.Unlock()
	st := caretakerState{PlantID: rw.plant.ID, Payouts: slices.Clone(rw.payouts)}
	if st.Payouts == nil {
		st.Payouts = []Payout{} // [] rather than null in the JSON
	}
	if !rw.wallet.IsZero() {
		st.Wallet = rw.wallet.String()
	}
	return st
}

// watch checks the latest reading every second and pays for each watering.
func (rw *Rewards) watch(sensors *LatestReading) {
	if !rw.On() {
		return
	}
	for range time.Tick(time.Second) {
		if r, ok := sensors.Get(); ok && !r.Stale() {
			if wallet, ok := rw.watered(r); ok {
				rw.pay(wallet, *r.MoisturePct)
			}
		}
	}
}

// watered reports whether reading r completes a watering that earns a
// reward, and if so, who gets it.
func (rw *Rewards) watered(r Reading) (solana.PublicKey, bool) {
	rw.mu.Lock()
	defer rw.mu.Unlock()
	if rw.wallet.IsZero() || r.MoisturePct == nil {
		return solana.PublicKey{}, false
	}
	m, p := *r.MoisturePct, rw.plant
	switch {
	case m < p.MoistureMinPct:
		rw.thirsty = true
	case m > p.MoistureMaxPct:
		rw.thirsty = false // overwatered: this watering doesn't count
	case rw.thirsty && m >= p.MoistureMinPct+wateredMarginPct:
		rw.thirsty = false
		if time.Since(rw.lastPaid) >= rewardCooldown {
			rw.lastPaid = time.Now()
			return rw.wallet, true
		}
	}
	return solana.PublicKey{}, false
}

// pay sends the reward to wallet and records it.
func (rw *Rewards) pay(wallet solana.PublicKey, moisturePct float64) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	sig, err := rw.send(ctx, wallet)
	if err != nil {
		var rpcErr *jsonrpc.RPCError
		if errors.As(err, &rpcErr) {
			err = errors.New(rpcErr.Message) // its Error() is a multi-line dump
		}
		log.Printf("rewards: paying %s failed: %v", wallet, err)
		return
	}
	p := Payout{
		At:          time.Now(),
		Wallet:      wallet.String(),
		SOL:         float64(rewardLamports) / float64(solana.LAMPORTS_PER_SOL),
		Signature:   sig.String(),
		ExplorerURL: "https://explorer.solana.com/tx/" + sig.String() + "?cluster=devnet",
	}
	log.Printf("rewards: watered (moisture %.0f%%), paid %g SOL to %s: %s", moisturePct, p.SOL, p.Wallet, p.ExplorerURL)
	rw.mu.Lock()
	defer rw.mu.Unlock()
	rw.payouts = append([]Payout{p}, rw.payouts...)
}

// send transfers rewardLamports from the treasury to wallet. It returns once
// the RPC node accepts the transaction, which lands a second or so later.
func (rw *Rewards) send(ctx context.Context, wallet solana.PublicKey) (solana.Signature, error) {
	recent, err := rw.rpc.GetLatestBlockhash(ctx, rpc.CommitmentFinalized)
	if err != nil {
		return solana.Signature{}, err
	}
	from := rw.treasury.PublicKey()
	tx, err := solana.NewTransaction(
		[]solana.Instruction{system.NewTransferInstruction(rewardLamports, from, wallet).Build()},
		recent.Value.Blockhash,
		solana.TransactionPayer(from),
	)
	if err != nil {
		return solana.Signature{}, err
	}
	_, err = tx.Sign(func(key solana.PublicKey) *solana.PrivateKey {
		if key.Equals(from) {
			return &rw.treasury
		}
		return nil
	})
	if err != nil {
		return solana.Signature{}, err
	}
	return rw.rpc.SendTransaction(ctx, tx)
}
