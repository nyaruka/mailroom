package models

import (
	"context"
	"fmt"
)

// SyncEventID is the type for sync event IDs
type SyncEventID int

// NilSyncEventID is the nil value for sync event IDs
const NilSyncEventID = SyncEventID(0)

// power source values reported by a relayer
const (
	SyncSourceAC       = "AC"
	SyncSourceUSB      = "USB"
	SyncSourceWireless = "WIR"
	SyncSourceBattery  = "BAT"
)

// power status values reported by a relayer
const (
	SyncStatusUnknown     = "UNK"
	SyncStatusCharging    = "CHA"
	SyncStatusDischarging = "DIS"
	SyncStatusNotCharging = "NOT"
	SyncStatusFull        = "FUL"
)

// SyncEvent is the record of a single sync by an Android channel's relayer, holding what the device reported about
// itself and how much work the sync did.
type SyncEvent struct {
	ID                   SyncEventID `db:"id"`
	ChannelID            ChannelID   `db:"channel_id"`
	PowerSource          string      `db:"power_source"`
	PowerStatus          string      `db:"power_status"`
	PowerLevel           int         `db:"power_level"`
	NetworkType          string      `db:"network_type"`
	PendingMessageCount  int         `db:"pending_message_count"`
	RetryMessageCount    int         `db:"retry_message_count"`
	IncomingCommandCount int         `db:"incoming_command_count"`
	OutgoingCommandCount int         `db:"outgoing_command_count"`
}

const sqlInsertSyncEvent = `
INSERT INTO channels_syncevent(channel_id, power_source, power_status, power_level, network_type, pending_message_count,
                               retry_message_count, incoming_command_count, outgoing_command_count, created_on)
     VALUES($1, $2, $3, $4, $5, $6, $7, $8, $9, NOW())
  RETURNING id`

// Insert writes this sync event, filling in its id.
func (e *SyncEvent) Insert(ctx context.Context, db DBorTx) error {
	err := db.GetContext(ctx, &e.ID, sqlInsertSyncEvent, e.ChannelID, e.PowerSource, e.PowerStatus, e.PowerLevel,
		e.NetworkType, e.PendingMessageCount, e.RetryMessageCount, e.IncomingCommandCount, e.OutgoingCommandCount)
	if err != nil {
		return fmt.Errorf("error inserting sync event: %w", err)
	}
	return nil
}

// UpdateOutgoingCommandCount records how many commands we ended up sending back, which isn't known until the rest of
// the sync has been processed.
func (e *SyncEvent) UpdateOutgoingCommandCount(ctx context.Context, db DBorTx, count int) error {
	e.OutgoingCommandCount = count

	_, err := db.ExecContext(ctx, `UPDATE channels_syncevent SET outgoing_command_count = $2 WHERE id = $1`, e.ID, count)
	if err != nil {
		return fmt.Errorf("error updating sync event command count: %w", err)
	}
	return nil
}
