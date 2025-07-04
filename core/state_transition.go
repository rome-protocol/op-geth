// Copyright 2014 The go-ethereum Authors
// This file is part of the go-ethereum library.
//
// The go-ethereum library is free software: you can redistribute it and/or modify
// it under the terms of the GNU Lesser General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// The go-ethereum library is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
// GNU Lesser General Public License for more details.
//
// You should have received a copy of the GNU Lesser General Public License
// along with the go-ethereum library. If not, see <http://www.gnu.org/licenses/>.

package core

import (
	"errors"
	"fmt"
	"math"
	"math/big"

	"github.com/ethereum/go-ethereum/common"
	cmath "github.com/ethereum/go-ethereum/common/math"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/core/vm"
	"github.com/ethereum/go-ethereum/params"
)

// ExecutionResult includes all output after executing given evm
// message no matter the execution itself is successful or not.
type ExecutionResult struct {
	UsedGas     uint64 // Total used gas, not including the refunded gas
	RefundedGas uint64 // Total gas refunded after execution
	Err         error  // Any error encountered during the execution(listed in core/vm/errors.go)
	ReturnData  []byte // Returned data from evm(function result or data supplied with revert opcode)
}

// Unwrap returns the internal evm error which allows us for further
// analysis outside.
func (result *ExecutionResult) Unwrap() error {
	return result.Err
}

// Failed returns the indicator whether the execution is successful or not
func (result *ExecutionResult) Failed() bool { return result.Err != nil }

// Return is a helper function to help caller distinguish between revert reason
// and function return. Return returns the data after execution if no error occurs.
func (result *ExecutionResult) Return() []byte {
	if result.Err != nil {
		return nil
	}
	return common.CopyBytes(result.ReturnData)
}

// Revert returns the concrete revert reason if the execution is aborted by `REVERT`
// opcode. Note the reason can be nil if no data supplied with revert opcode.
func (result *ExecutionResult) Revert() []byte {
	if result.Err != vm.ErrExecutionReverted {
		return nil
	}
	return common.CopyBytes(result.ReturnData)
}

// IntrinsicGas computes the 'intrinsic gas' for a message with the given data.
func IntrinsicGas(data []byte, accessList types.AccessList, isContractCreation bool, isHomestead, isEIP2028 bool, isEIP3860 bool) (uint64, error) {
	return 0, nil
}

// A Message contains the data derived from a single transaction that is relevant to state
// processing.
type Message struct {
	To            *common.Address
	From          common.Address
	Nonce         uint64
	Value         *big.Int
	GasLimit      uint64
	GasPrice      *big.Int
	GasFeeCap     *big.Int
	GasTipCap     *big.Int
	Data          []byte
	AccessList    types.AccessList
	BlobGasFeeCap *big.Int
	BlobHashes    []common.Hash

	// When SkipAccountChecks is true, the message nonce is not checked against the
	// account nonce in state. It also disables checking that the sender is an EOA.
	// This field will be set to true for operations like RPC eth_call.
	SkipAccountChecks bool

	IsSystemTx     bool                 // IsSystemTx indicates the message, if also a deposit, does not emit gas usage.
	IsDepositTx    bool                 // IsDepositTx indicates the message is force-included and can persist a mint.
	Mint           *big.Int             // Mint is the amount to mint before EVM processing, or nil if there is no minting.
	RollupCostData types.RollupCostData // RollupCostData caches data to compute the fee we charge for data availability
}

// TransactionToMessage converts a transaction into a Message.
func TransactionToMessage(tx *types.Transaction, s types.Signer, baseFee *big.Int) (*Message, error) {
	msg := &Message{
		Nonce:          tx.Nonce(),
		GasLimit:       tx.Gas(),
		GasPrice:       new(big.Int).Set(tx.GasPrice()),
		GasFeeCap:      new(big.Int).Set(tx.GasFeeCap()),
		GasTipCap:      new(big.Int).Set(tx.GasTipCap()),
		To:             tx.To(),
		Value:          tx.Value(),
		Data:           tx.Data(),
		AccessList:     tx.AccessList(),
		IsSystemTx:     tx.IsSystemTx(),
		IsDepositTx:    tx.IsDepositTx(),
		Mint:           tx.Mint(),
		RollupCostData: tx.RollupCostData(),

		SkipAccountChecks: false,
		BlobHashes:        tx.BlobHashes(),
		BlobGasFeeCap:     tx.BlobGasFeeCap(),
	}
	// If baseFee provided, set gasPrice to effectiveGasPrice.
	if baseFee != nil {
		msg.GasPrice = cmath.BigMin(msg.GasPrice.Add(msg.GasTipCap, baseFee), msg.GasFeeCap)
	}
	var err error
	msg.From, err = types.Sender(s, tx)
	return msg, err
}

// ApplyMessage computes the new state by applying the given message
// against the old state within the environment.
//
// ApplyMessage returns the bytes returned by any EVM execution (if it took place),
// the gas used (which includes gas refunds) and an error if it failed. An error always
// indicates a core error meaning that the message would always fail for that particular
// state and would never be accepted within a block.
func ApplyMessage(evm *vm.EVM, msg *Message, gp *GasPool, romeGasUsed uint64) (*ExecutionResult, error) {
	return NewStateTransition(evm, msg, gp).TransitionDb(romeGasUsed)
}

// StateTransition represents a state transition.
//
// == The State Transitioning Model
//
// A state transition is a change made when a transaction is applied to the current world
// state. The state transitioning model does all the necessary work to work out a valid new
// state root.
//
//  1. Nonce handling
//  2. Pre pay gas
//  3. Create a new state object if the recipient is nil
//  4. Value transfer
//
// == If contract creation ==
//
//	4a. Attempt to run transaction data
//	4b. If valid, use result as code for the new state object
//
// == end ==
//
//  5. Run Script section
//  6. Derive new state root
type StateTransition struct {
	gp           *GasPool
	msg          *Message
	gasRemaining uint64
	initialGas   uint64
	state        vm.StateDB
	evm          *vm.EVM
}

// NewStateTransition initialises and returns a new state transition object.
func NewStateTransition(evm *vm.EVM, msg *Message, gp *GasPool) *StateTransition {
	return &StateTransition{
		gp:    gp,
		evm:   evm,
		msg:   msg,
		state: evm.StateDB,
	}
}

// to returns the recipient of the message.
func (st *StateTransition) to() common.Address {
	if st.msg == nil || st.msg.To == nil /* contract creation */ {
		return common.Address{}
	}
	return *st.msg.To
}

func (st *StateTransition) buyGas(romeGasUsed uint64) error {
	zeroAddress := common.Address{}
	if st.evm.Context.Coinbase == zeroAddress {
		return nil
	}

	mgval := new(big.Int).SetUint64(romeGasUsed)
	if st.msg.GasTipCap != nil {
		mgval = mgval.Mul(mgval, st.msg.GasTipCap)
	} else {
		mgval = mgval.Mul(mgval, st.msg.GasPrice)
	}
	balanceCheck := new(big.Int).Set(mgval)
	if have, want := st.state.GetBalance(st.msg.From), balanceCheck; have.Cmp(want) < 0 {
		return fmt.Errorf("%w: address %v have %v want %v", ErrInsufficientFunds, st.msg.From.Hex(), have, want)
	}
	if err := st.gp.SubGas(romeGasUsed); err != nil {
		return err
	}
	st.gasRemaining += math.MaxUint64 / 2

	st.initialGas = math.MaxUint64 / 2
	st.state.SubBalance(st.msg.From, mgval)

	return nil
}

func (st *StateTransition) preCheck(romeGasUsed uint64) error {
	if st.msg.IsDepositTx {
		// No fee fields to check, no nonce to check, and no need to check if EOA (L1 already verified it for us)
		// Gas is free, but no refunds!
		st.initialGas = st.msg.GasLimit
		st.gasRemaining += st.msg.GasLimit // Add gas here in order to be able to execute calls.
		// Don't touch the gas pool for system transactions
		if st.msg.IsSystemTx {
			if st.evm.ChainConfig().IsOptimismRegolith(st.evm.Context.Time) {
				return fmt.Errorf("%w: address %v", ErrSystemTxNotSupported,
					st.msg.From.Hex())
			}
			return nil
		}
		return st.gp.SubGas(st.msg.GasLimit) // gas used by deposits may not be used by other txs
	}
	// Only check transactions that are not fake
	msg := st.msg
	if !msg.SkipAccountChecks {
		// Make sure this transaction's nonce is correct.
		stNonce := st.state.GetNonce(msg.From)
		if msgNonce := msg.Nonce; stNonce < msgNonce {
			return fmt.Errorf("%w: address %v, tx: %d state: %d", ErrNonceTooHigh,
				msg.From.Hex(), msgNonce, stNonce)
		} else if stNonce > msgNonce {
			return fmt.Errorf("%w: address %v, tx: %d state: %d", ErrNonceTooLow,
				msg.From.Hex(), msgNonce, stNonce)
		} else if stNonce+1 < stNonce {
			return fmt.Errorf("%w: address %v, nonce: %d", ErrNonceMax,
				msg.From.Hex(), stNonce)
		}
		// Make sure the sender is an EOA
		codeHash := st.state.GetCodeHash(msg.From)
		if codeHash != (common.Hash{}) && codeHash != types.EmptyCodeHash {
			return fmt.Errorf("%w: address %v, codehash: %s", ErrSenderNoEOA,
				msg.From.Hex(), codeHash)
		}
	}
	// Check the blob version validity
	if msg.BlobHashes != nil {
		if len(msg.BlobHashes) == 0 {
			return errors.New("blob transaction missing blob hashes")
		}
		for i, hash := range msg.BlobHashes {
			if hash[0] != params.BlobTxHashVersion {
				return fmt.Errorf("blob %d hash version mismatch (have %d, supported %d)",
					i, hash[0], params.BlobTxHashVersion)
			}
		}
	}
	// Check that the user is paying at least the current blob fee
	if st.evm.ChainConfig().IsCancun(st.evm.Context.BlockNumber, st.evm.Context.Time) {
		if st.blobGasUsed() > 0 {
			// Skip the checks if gas fields are zero and blobBaseFee was explicitly disabled (eth_call)
			skipCheck := st.evm.Config.NoBaseFee && msg.BlobGasFeeCap.BitLen() == 0
			if !skipCheck {
				// This will panic if blobBaseFee is nil, but blobBaseFee presence
				// is verified as part of header validation.
				if msg.BlobGasFeeCap.Cmp(st.evm.Context.BlobBaseFee) < 0 {
					return fmt.Errorf("%w: address %v blobGasFeeCap: %v, blobBaseFee: %v", ErrBlobFeeCapTooLow,
						msg.From.Hex(), msg.BlobGasFeeCap, st.evm.Context.BlobBaseFee)
				}
			}
		}
	}
	return st.buyGas(romeGasUsed)
}

// TransitionDb will transition the state by applying the current message and
// returning the evm execution result with following fields.
//
//   - used gas: total gas used (including gas being refunded)
//   - returndata: the returned data from evm
//   - concrete execution error: various EVM errors which abort the execution, e.g.
//     ErrOutOfGas, ErrExecutionReverted
//
// However if any consensus issue encountered, return the error directly with
// nil evm execution result.
func (st *StateTransition) TransitionDb(romeGasUsed uint64) (*ExecutionResult, error) {
	if mint := st.msg.Mint; mint != nil {
		st.state.AddBalance(st.msg.From, mint)
	}
	snap := st.state.Snapshot()

	result, err := st.innerTransitionDb(romeGasUsed)
	// Failed deposits must still be included. Unless we cannot produce the block at all due to the gas limit.
	// On deposit failure, we rewind any state changes from after the minting, and increment the nonce.
	if err != nil && err != ErrGasLimitReached && st.msg.IsDepositTx {
		st.state.RevertToSnapshot(snap)
		// Even though we revert the state changes, always increment the nonce for the next deposit transaction
		st.state.SetNonce(st.msg.From, st.state.GetNonce(st.msg.From)+1)
		// Record deposits as using all their gas (matches the gas pool)
		// System Transactions are special & are not recorded as using any gas (anywhere)
		// Regolith changes this behaviour so the actual gas used is reported.
		// In this case the tx is invalid so is recorded as using all gas.
		gasUsed := st.msg.GasLimit
		if st.msg.IsSystemTx && !st.evm.ChainConfig().IsRegolith(st.evm.Context.Time) {
			gasUsed = 0
		}
		result = &ExecutionResult{
			UsedGas:    gasUsed,
			Err:        fmt.Errorf("failed deposit: %w", err),
			ReturnData: nil,
		}
		err = nil
	}
	return result, err
}

func (st *StateTransition) innerTransitionDb(romeGasUsed uint64) (*ExecutionResult, error) {
	// First check this message satisfies all consensus rules before
	// applying the message. The rules include these clauses
	//
	// 1. the nonce of the message caller is correct
	// 2. caller has enough balance to cover transaction fee(gaslimit * gasprice)
	// 3. the amount of gas required is available in the block
	// 4. the purchased gas is enough to cover intrinsic usage
	// 5. there is no overflow when calculating intrinsic gas
	// 6. caller has enough balance to cover asset transfer for **topmost** call

	// Check clauses 1-3, buy gas if everything is correct
	if err := st.preCheck(romeGasUsed); err != nil {
		return nil, err
	}

	if tracer := st.evm.Config.Tracer; tracer != nil {
		tracer.CaptureTxStart(st.initialGas)
		defer func() {
			tracer.CaptureTxEnd(st.gasRemaining)
		}()
	}

	var (
		msg              = st.msg
		sender           = vm.AccountRef(msg.From)
		rules            = st.evm.ChainConfig().Rules(st.evm.Context.BlockNumber, st.evm.Context.Random != nil, st.evm.Context.Time)
		contractCreation = msg.To == nil
	)

	// Check clauses 4-5, subtract intrinsic gas if everything is correct
	gas, err := IntrinsicGas(msg.Data, msg.AccessList, contractCreation, rules.IsHomestead, rules.IsIstanbul, rules.IsShanghai)
	if err != nil {
		return nil, err
	}
	if st.gasRemaining < gas {
		return nil, fmt.Errorf("%w: have %d, want %d", ErrIntrinsicGas, st.gasRemaining, gas)
	}
	st.gasRemaining -= gas

	// Execute the preparatory steps for state transition which includes:
	// - prepare accessList(post-berlin)
	// - reset transient storage(eip 1153)
	st.state.Prepare(rules, msg.From, st.evm.Context.Coinbase, msg.To, vm.ActivePrecompiles(rules), msg.AccessList)

	var (
		ret   []byte
		vmerr error // vm errors do not effect consensus and are therefore not assigned to err
	)
	if contractCreation {
		ret, _, st.gasRemaining, vmerr = st.evm.Create(sender, msg.Data, st.gasRemaining, msg.Value)
	} else {
		// Increment the nonce for the next transaction
		st.state.SetNonce(msg.From, st.state.GetNonce(sender.Address())+1)
		ret, st.gasRemaining, vmerr = st.evm.Call(sender, st.to(), msg.Data, st.gasRemaining, msg.Value)
	}

	// if deposit: skip refunds, skip tipping coinbase
	// Regolith changes this behaviour to report the actual gasUsed instead of always reporting all gas used.
	if st.msg.IsDepositTx && !rules.IsOptimismRegolith {
		// Record deposits as using all their gas (matches the gas pool)
		// System Transactions are special & are not recorded as using any gas (anywhere)
		return &ExecutionResult{
			UsedGas:    romeGasUsed,
			Err:        vmerr,
			ReturnData: ret,
		}, nil
	}
	if st.msg.IsDepositTx && rules.IsOptimismRegolith {
		// Skip coinbase payments for deposit tx in Regolith
		return &ExecutionResult{
			UsedGas:     romeGasUsed,
			RefundedGas: 0,
			Err:         vmerr,
			ReturnData:  ret,
		}, nil
	}

	effectiveTip := msg.GasPrice
	if msg.GasTipCap != nil {
		effectiveTip = msg.GasTipCap
	}

	if st.evm.Config.NoBaseFee && msg.GasFeeCap.Sign() == 0 && msg.GasTipCap.Sign() == 0 {
		// Skip fee payment when NoBaseFee is set and the fee fields
		// are 0. This avoids a negative effectiveTip being applied to
		// the coinbase when simulating calls.
	} else {
		fee := new(big.Int).SetUint64(romeGasUsed)
		fee.Mul(fee, effectiveTip)
		zeroAddress := common.Address{}
		if st.evm.Context.Coinbase != zeroAddress {
			st.state.AddBalance(st.evm.Context.Coinbase, fee)
		}
	}

	// Check that we are post bedrock to enable op-geth to be able to create pseudo pre-bedrock blocks (these are pre-bedrock, but don't follow l2 geth rules)
	// Note optimismConfig will not be nil if rules.IsOptimismBedrock is true
	if optimismConfig := st.evm.ChainConfig().Optimism; optimismConfig != nil && rules.IsOptimismBedrock && !st.msg.IsDepositTx {
		st.state.AddBalance(params.OptimismBaseFeeRecipient, new(big.Int).Mul(new(big.Int).SetUint64(st.gasUsed()), st.evm.Context.BaseFee))
		if cost := st.evm.Context.L1CostFunc(st.msg.RollupCostData, st.evm.Context.Time); cost != nil {
			st.state.AddBalance(params.OptimismL1FeeRecipient, cost)
		}
	}

	return &ExecutionResult{
		UsedGas:     romeGasUsed,
		RefundedGas: 0,
		Err:         vmerr,
		ReturnData:  ret,
	}, nil
}

func (st *StateTransition) refundGas(refundQuotient uint64) uint64 {
	// Apply refund counter, capped to a refund quotient
	refund := st.gasUsed() / refundQuotient
	if refund > st.state.GetRefund() {
		refund = st.state.GetRefund()
	}
	st.gasRemaining += refund

	// Return ETH for remaining gas, exchanged at the original rate.
	remaining := new(big.Int).Mul(new(big.Int).SetUint64(st.gasRemaining), st.msg.GasPrice)
	zeroAddress := common.Address{}
	if st.evm.Context.Coinbase != zeroAddress {
		st.state.AddBalance(st.msg.From, remaining)
	}

	// Also return remaining gas to the block gas counter so it is
	// available for the next transaction.
	//st.gp.AddGas(st.gasRemaining)

	return refund
}

// gasUsed returns the amount of gas used up by the state transition.
func (st *StateTransition) gasUsed() uint64 {
	return st.initialGas - st.gasRemaining
}

// blobGasUsed returns the amount of blob gas used by the message.
func (st *StateTransition) blobGasUsed() uint64 {
	return uint64(len(st.msg.BlobHashes) * params.BlobTxBlobGasPerBlob)
}
