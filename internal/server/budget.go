package server

import "github.com/eukalpia/unio/internal/bufpool"

// budgets are the node's buffer budgets, one per layer of the data path.
//
// A request holds buffers of several layers at once: the S3 front end holds a
// copy buffer while the engine reads, an erasure set holds a frame while it
// writes its shards, and each shard write takes a transport or drive buffer.
// If two layers drew from one budget, a layer could hold the last buffers
// while waiting for one that only it could release, and under load every
// request would wait until a stall timeout declared healthy drives dead. With
// one budget per layer, a layer waits only on layers below it (front end,
// engine, erasure sets, transport, drives), so the waits can never form a
// cycle. The transport has two budgets: streams this node sends wait on
// peers, so the replies it serves to peers must never wait on the buffers
// those streams hold, or two nodes sending to each other could stall both.
type budgets struct {
	front          *bufpool.Budget
	engine         *bufpool.Budget
	erasure        *bufpool.Budget
	transportOut   *bufpool.Budget
	transportServe *bufpool.Budget
	drives         *bufpool.Budget
}

// Shares of the memory budget, in percent. Erasure frames and drive buffers
// carry every byte of data, so they get most of it; the engine only buffers
// inline objects of at most 64 KiB. The configuration's minimum budget of
// 64 MiB leaves every layer room for its largest buffer.
const (
	frontShare     = 20
	engineShare    = 5
	erasureShare   = 35
	transportShare = 10 // split evenly between sending and serving
	drivesShare    = 30
)

// splitBudget divides total bytes between the layers.
func splitBudget(total int64) budgets {
	part := func(share int64) *bufpool.Budget { return bufpool.NewBudget(total / 100 * share) }
	return budgets{
		front:          part(frontShare),
		engine:         part(engineShare),
		erasure:        part(erasureShare),
		transportOut:   part(transportShare / 2),
		transportServe: part(transportShare / 2),
		drives:         part(drivesShare),
	}
}

// inUse returns the bytes held in every layer.
func (b budgets) inUse() int64 {
	return b.front.InUse() + b.engine.InUse() + b.erasure.InUse() + b.transportOut.InUse() + b.transportServe.InUse() + b.drives.InUse()
}
