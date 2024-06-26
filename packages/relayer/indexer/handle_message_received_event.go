package indexer

import (
	"context"
	"encoding/json"
	"log/slog"
	"math/big"

	"github.com/pkg/errors"
	"github.com/taikoxyz/taiko-mono/packages/relayer"
	"github.com/taikoxyz/taiko-mono/packages/relayer/bindings/bridge"
	"github.com/taikoxyz/taiko-mono/packages/relayer/pkg/queue"
)

// handleMessageProcessedEvent handles an individual MessageProcessed event
func (i *Indexer) handleMessageProcessedEvent(
	ctx context.Context,
	chainID *big.Int,
	event *bridge.BridgeMessageProcessed,
	waitForConfirmations bool,
) error {
	slog.Info("msg received event found",
		"txHash", event.Raw.TxHash.Hex(),
	)

	// if the destination doesnt match our source chain, we dont want to handle this event.
	if new(big.Int).SetUint64(event.Message.DestChainId).Cmp(i.srcChainId) != 0 {
		slog.Info("skipping event, wrong chainID",
			"messageDestChainID",
			event.Message.DestChainId,
			"indexerSrcChainID",
			i.srcChainId.Uint64(),
		)

		return nil
	}

	if event.Raw.Removed {
		slog.Info("event is removed")
		return nil
	}

	if waitForConfirmations {
		// we need to wait for confirmations to confirm this event is not being reverted,
		// removed, or reorged now.
		confCtx, confCtxCancel := context.WithTimeout(ctx, defaultCtxTimeout)

		defer confCtxCancel()

		if err := relayer.WaitConfirmations(
			confCtx,
			i.srcEthClient,
			uint64(defaultConfirmations),
			event.Raw.TxHash,
		); err != nil {
			return err
		}
	}

	// if the message is not status new, and we are iterating crawling past blocks,
	// we also dont want to handle this event. it has already been handled.
	if i.watchMode == CrawlPastBlocks {
		// we can return early, this message has been processed as expected.
		return nil
	}

	marshaled, err := json.Marshal(event)
	if err != nil {
		return errors.Wrap(err, "json.Marshal(event)")
	}

	id, err := i.saveEventToDB(
		ctx,
		marshaled,
		"0x",
		chainID,
		1,
		event.Message.SrcOwner.Hex(),
		event.Message.Data,
		event.Message.Value,
		event.Raw.BlockNumber,
	)
	if err != nil {
		return errors.Wrap(err, "i.saveEventToDB")
	}

	msg := queue.QueueMessageProcessedBody{
		ID:    id,
		Event: event,
	}

	marshalledMsg, err := json.Marshal(msg)
	if err != nil {
		return errors.Wrap(err, "json.Marshal")
	}

	// we add it to the queue, so the processor can pick up and attempt to process
	// the message onchain.
	if err := i.queue.Publish(ctx, i.queueName(), marshalledMsg, nil); err != nil {
		return errors.Wrap(err, "i.queue.Publish")
	}

	return nil
}
