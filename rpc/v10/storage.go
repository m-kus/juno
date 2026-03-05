package rpcv10

import (
	"errors"

	"github.com/NethermindEth/juno/core"
	"github.com/NethermindEth/juno/core/felt"
	"github.com/NethermindEth/juno/db"
	"github.com/NethermindEth/juno/jsonrpc"
	"github.com/NethermindEth/juno/rpc/rpccore"
	rpcv9 "github.com/NethermindEth/juno/rpc/v9"
	"go.uber.org/zap"
)

type StorageResult struct {
	Value           *felt.Felt `json:"value"`
	LastUpdateBlock uint64     `json:"last_update_block"`
}

// StorageAt gets the value of the storage at the given address and key.
// When responseFlags includes INCLUDE_LAST_UPDATE_BLOCK, the response is a StorageResult
// object containing the value and the block at which it was last modified.
func (h *Handler) StorageAt(address, key *felt.Felt, id *rpcv9.BlockID, responseFlags ResponseFlags) (any, *jsonrpc.Error) {
	stateReader, stateCloser, rpcErr := h.stateByBlockID(id)
	if rpcErr != nil {
		return nil, rpcErr
	}
	defer h.callAndLogErr(stateCloser, "Error closing state reader in getStorageAt")

	// Check if the contract exists
	_, err := stateReader.ContractClassHash(address)
	if err != nil {
		if errors.Is(err, db.ErrKeyNotFound) {
			return nil, rpccore.ErrContractNotFound
		}
		h.log.Error("Failed to get contract class hash", zap.Error(err))
		return nil, rpccore.ErrInternal
	}

	value, err := stateReader.ContractStorage(address, key)
	if err != nil {
		return nil, rpccore.ErrInternal
	}

	if !responseFlags.IncludeLastUpdateBlock {
		return &value, nil
	}

	// Resolve block number for last-update lookup
	lastUpdateBlock, rpcErr := h.resolveLastUpdateBlock(address, key, id)
	if rpcErr != nil {
		return nil, rpcErr
	}

	return &StorageResult{
		Value:           &value,
		LastUpdateBlock: lastUpdateBlock,
	}, nil
}

// resolveLastUpdateBlock determines the block at which the storage key was last modified.
func (h *Handler) resolveLastUpdateBlock(address, key *felt.Felt, id *rpcv9.BlockID) (uint64, *jsonrpc.Error) {
	if id.IsPreConfirmed() {
		return h.resolveLastUpdateBlockPreConfirmed(address, key)
	}

	blockNum, rpcErr := h.resolveBlockNumber(id)
	if rpcErr != nil {
		return 0, rpcErr
	}

	_, lastUpdate, err := h.bcReader.ContractStorageLastUpdate(address, key, blockNum)
	if err != nil {
		h.log.Error("Failed to get contract storage last update", zap.Error(err))
		return 0, rpccore.ErrInternal
	}

	return lastUpdate, nil
}

func (h *Handler) resolveLastUpdateBlockPreConfirmed(address, key *felt.Felt) (uint64, *jsonrpc.Error) {
	pending, err := h.PendingData()
	if err != nil {
		if errors.Is(err, core.ErrPendingDataNotFound) {
			return 0, rpccore.ErrBlockNotFound
		}
		return 0, rpccore.ErrInternal.CloneWithData(err)
	}

	// Check pre_confirmed's own (non-aggregated) state diff
	su := pending.GetStateUpdate()
	if su != nil && su.StateDiff != nil {
		if addrDiffs, ok := su.StateDiff.StorageDiffs[*address]; ok {
			if _, ok := addrDiffs[*key]; ok {
				return pending.GetBlock().Number, nil
			}
		}
	}

	// Check PreLatest if it exists (pre_confirmed is N+2 while latest is N)
	preLatest := pending.GetPreLatest()
	if preLatest != nil && preLatest.StateUpdate != nil && preLatest.StateUpdate.StateDiff != nil {
		if addrDiffs, ok := preLatest.StateUpdate.StateDiff.StorageDiffs[*address]; ok {
			if _, ok := addrDiffs[*key]; ok {
				return preLatest.Block.Number, nil
			}
		}
	}

	// Fall through to DB history
	// Get the head block number as the upper bound for the history query
	height, err := h.bcReader.Height()
	if err != nil {
		return 0, rpccore.ErrInternal.CloneWithData(err)
	}

	_, lastUpdate, err := h.bcReader.ContractStorageLastUpdate(address, key, height)
	if err != nil {
		h.log.Error("Failed to get contract storage last update", zap.Error(err))
		return 0, rpccore.ErrInternal
	}

	return lastUpdate, nil
}

func (h *Handler) resolveBlockNumber(id *rpcv9.BlockID) (uint64, *jsonrpc.Error) {
	switch {
	case id.IsLatest():
		height, err := h.bcReader.Height()
		if err != nil {
			return 0, rpccore.ErrInternal.CloneWithData(err)
		}
		return height, nil
	case id.IsHash():
		num, err := h.bcReader.BlockNumberByHash(id.Hash())
		if err != nil {
			if errors.Is(err, db.ErrKeyNotFound) {
				return 0, rpccore.ErrBlockNotFound
			}
			return 0, rpccore.ErrInternal.CloneWithData(err)
		}
		return num, nil
	case id.IsNumber():
		return id.Number(), nil
	case id.IsL1Accepted():
		l1Head, err := h.bcReader.L1Head()
		if err != nil {
			return 0, rpccore.ErrInternal.CloneWithData(err)
		}
		return l1Head.BlockNumber, nil
	default:
		return 0, rpccore.ErrInternal.CloneWithData("unknown block id type")
	}
}
