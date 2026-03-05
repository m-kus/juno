package rpcv10_test

import (
	"errors"
	"fmt"
	"testing"

	"github.com/NethermindEth/juno/core"
	"github.com/NethermindEth/juno/core/felt"
	"github.com/NethermindEth/juno/db"
	"github.com/NethermindEth/juno/mocks"
	"github.com/NethermindEth/juno/rpc/rpccore"
	rpcv10 "github.com/NethermindEth/juno/rpc/v10"
	rpcv9 "github.com/NethermindEth/juno/rpc/v9"
	"github.com/NethermindEth/juno/utils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
)

func blockIDNumber(t *testing.T, val uint64) rpcv9.BlockID {
	t.Helper()
	blockID := rpcv9.BlockID{}
	require.NoError(t, blockID.UnmarshalJSON([]byte(fmt.Sprintf(`{"block_number":%d}`, val))))
	return blockID
}

func TestStorageAt(t *testing.T) {
	mockCtrl := gomock.NewController(t)
	t.Cleanup(mockCtrl.Finish)

	mockReader := mocks.NewMockReader(mockCtrl)
	mockSyncReader := mocks.NewMockSyncReader(mockCtrl)
	log := utils.NewNopZapLogger()
	handler := rpcv10.New(mockReader, mockSyncReader, nil, log)

	targetAddress := felt.FromUint64[felt.Felt](1234)
	targetSlot := felt.FromUint64[felt.Felt](5678)
	mockState := mocks.NewMockStateReader(mockCtrl)

	t.Run("no flags returns plain felt", func(t *testing.T) {
		expectedStorage := felt.NewFromUint64[felt.Felt](42)
		mockReader.EXPECT().HeadState().Return(mockState, nopCloser, nil)
		mockState.EXPECT().ContractClassHash(&targetAddress).Return(felt.Felt{}, nil)
		mockState.EXPECT().ContractStorage(&targetAddress, &targetSlot).Return(*expectedStorage, nil)

		blockID := rpcv9.BlockIDLatest()
		result, rpcErr := handler.StorageAt(&targetAddress, &targetSlot, &blockID, rpcv10.ResponseFlags{})
		require.Nil(t, rpcErr)
		assert.Equal(t, expectedStorage, result)
	})

	t.Run("INCLUDE_LAST_UPDATE_BLOCK returns StorageResult", func(t *testing.T) {
		expectedStorage := felt.NewFromUint64[felt.Felt](42)
		mockReader.EXPECT().HeadState().Return(mockState, nopCloser, nil)
		mockState.EXPECT().ContractClassHash(&targetAddress).Return(felt.Felt{}, nil)
		mockState.EXPECT().ContractStorage(&targetAddress, &targetSlot).Return(*expectedStorage, nil)
		mockReader.EXPECT().Height().Return(uint64(10), nil)
		mockReader.EXPECT().ContractStorageLastUpdate(&targetAddress, &targetSlot, uint64(10)).
			Return(*expectedStorage, uint64(7), nil)

		blockID := rpcv9.BlockIDLatest()
		flags := rpcv10.ResponseFlags{IncludeLastUpdateBlock: true}
		result, rpcErr := handler.StorageAt(&targetAddress, &targetSlot, &blockID, flags)
		require.Nil(t, rpcErr)

		storageResult, ok := result.(*rpcv10.StorageResult)
		require.True(t, ok)
		assert.Equal(t, expectedStorage, storageResult.Value)
		assert.Equal(t, uint64(7), storageResult.LastUpdateBlock)
	})

	t.Run("non-existent contract", func(t *testing.T) {
		mockReader.EXPECT().HeadState().Return(mockState, nopCloser, nil)
		mockState.EXPECT().ContractClassHash(&targetAddress).Return(felt.Felt{}, db.ErrKeyNotFound)

		blockID := rpcv9.BlockIDLatest()
		result, rpcErr := handler.StorageAt(&targetAddress, &targetSlot, &blockID, rpcv10.ResponseFlags{})
		require.Nil(t, result)
		assert.Equal(t, rpccore.ErrContractNotFound, rpcErr)
	})

	t.Run("non-existent key with flag returns zero value and block 0", func(t *testing.T) {
		mockReader.EXPECT().HeadState().Return(mockState, nopCloser, nil)
		mockState.EXPECT().ContractClassHash(&targetAddress).Return(felt.Felt{}, nil)
		mockState.EXPECT().ContractStorage(&targetAddress, &targetSlot).Return(felt.Zero, nil)
		mockReader.EXPECT().Height().Return(uint64(10), nil)
		mockReader.EXPECT().ContractStorageLastUpdate(&targetAddress, &targetSlot, uint64(10)).
			Return(felt.Zero, uint64(0), nil)

		blockID := rpcv9.BlockIDLatest()
		flags := rpcv10.ResponseFlags{IncludeLastUpdateBlock: true}
		result, rpcErr := handler.StorageAt(&targetAddress, &targetSlot, &blockID, flags)
		require.Nil(t, rpcErr)

		storageResult, ok := result.(*rpcv10.StorageResult)
		require.True(t, ok)
		assert.Equal(t, &felt.Zero, storageResult.Value)
		assert.Equal(t, uint64(0), storageResult.LastUpdateBlock)
	})

	t.Run("block number with flag", func(t *testing.T) {
		expectedStorage := felt.NewFromUint64[felt.Felt](99)
		mockReader.EXPECT().StateAtBlockNumber(uint64(5)).Return(mockState, nopCloser, nil)
		mockState.EXPECT().ContractClassHash(&targetAddress).Return(felt.Felt{}, nil)
		mockState.EXPECT().ContractStorage(&targetAddress, &targetSlot).Return(*expectedStorage, nil)
		mockReader.EXPECT().ContractStorageLastUpdate(&targetAddress, &targetSlot, uint64(5)).
			Return(*expectedStorage, uint64(3), nil)

		blockID := blockIDNumber(t, 5)
		flags := rpcv10.ResponseFlags{IncludeLastUpdateBlock: true}
		result, rpcErr := handler.StorageAt(&targetAddress, &targetSlot, &blockID, flags)
		require.Nil(t, rpcErr)

		storageResult, ok := result.(*rpcv10.StorageResult)
		require.True(t, ok)
		assert.Equal(t, expectedStorage, storageResult.Value)
		assert.Equal(t, uint64(3), storageResult.LastUpdateBlock)
	})

	t.Run("pre_confirmed with key in pending diff", func(t *testing.T) {
		pendingValue := felt.NewFromUint64[felt.Felt](77)
		parentHash := felt.NewFromUint64[felt.Felt](999)
		pendingBlock := &core.Block{
			Header: &core.Header{Number: 11, ParentHash: parentHash},
		}
		pendingStateDiff := &core.StateDiff{
			StorageDiffs: map[felt.Felt]map[felt.Felt]*felt.Felt{
				targetAddress: {targetSlot: pendingValue},
			},
		}
		pendingData := &core.Pending{
			Block:       pendingBlock,
			StateUpdate: &core.StateUpdate{StateDiff: pendingStateDiff},
		}

		// PendingState() calls syncReader.PendingData(), then resolves base state via StateAtBlockHash
		mockSyncReader.EXPECT().PendingData().Return(pendingData, nil).Times(2)
		mockReader.EXPECT().StateAtBlockHash(parentHash).Return(mockState, nopCloser, nil)
		mockState.EXPECT().ContractClassHash(&targetAddress).Return(felt.Felt{}, nil)

		blockID := rpcv9.BlockIDPreConfirmed()
		flags := rpcv10.ResponseFlags{IncludeLastUpdateBlock: true}
		result, rpcErr := handler.StorageAt(&targetAddress, &targetSlot, &blockID, flags)
		require.Nil(t, rpcErr)

		storageResult, ok := result.(*rpcv10.StorageResult)
		require.True(t, ok)
		assert.Equal(t, pendingValue, storageResult.Value)
		assert.Equal(t, uint64(11), storageResult.LastUpdateBlock)
	})

	t.Run("empty blockchain", func(t *testing.T) {
		mockReader.EXPECT().HeadState().Return(nil, nil, db.ErrKeyNotFound)

		blockID := rpcv9.BlockIDLatest()
		result, rpcErr := handler.StorageAt(&targetAddress, &targetSlot, &blockID, rpcv10.ResponseFlags{})
		require.Nil(t, result)
		assert.Equal(t, rpccore.ErrBlockNotFound, rpcErr)
	})

	t.Run("internal error while retrieving key", func(t *testing.T) {
		mockReader.EXPECT().HeadState().Return(mockState, nopCloser, nil)
		mockState.EXPECT().ContractClassHash(&targetAddress).Return(felt.Felt{}, nil)
		mockState.EXPECT().ContractStorage(&targetAddress, &targetSlot).
			Return(felt.Felt{}, errors.New("some internal error"))

		blockID := rpcv9.BlockIDLatest()
		result, rpcErr := handler.StorageAt(&targetAddress, &targetSlot, &blockID, rpcv10.ResponseFlags{})
		assert.Nil(t, result)
		assert.Equal(t, rpccore.ErrInternal, rpcErr)
	})
}
