package opcda

import (
	"context"
	"testing"
	"time"

	"github.com/oiweiwei/go-msrpc/dcerpc"
	"github.com/oiweiwei/go-msrpc/ndr"
	statemgt "github.com/oiweiwei/go-opcda/opc/opcda/iopcgroupstatemgt/v0"
)

type activeStateConn struct {
	dcerpc.Conn
	request statemgt.SetStateRequest
}

func (c *activeStateConn) Invoke(
	_ context.Context,
	op dcerpc.Operation,
	_ ...dcerpc.CallOption,
) error {
	wire, err := ndr.Marshal(ndr.MarshalNDRFunc(op.MarshalNDRRequest))
	if err != nil {
		return err
	}
	if err := ndr.Unmarshal(wire, &c.request); err != nil {
		return err
	}
	// The update-rate output is not relevant when no rate was requested.
	op.(*opcOp).resp.(*groupStateResponse).rate = 0
	return nil
}

func TestSetActivePreservesGroupSamplingProperties(t *testing.T) {
	c, persistent, _ := testPersistent()
	wire := &activeStateConn{}
	persistent.stateConn = wire
	persistent.rate = 600
	s := &Server{ctx: t.Context(), conn: c}
	g := &Group{server: s, id: 1, updateRateMs: 600}
	for _, active := range []bool{false, true} {
		if err := g.SetActive(t.Context(), active); err != nil {
			t.Fatal(err)
		}
		wantMask := statemgt.SetStateNullMaskRequestAll &^ statemgt.SetStateNullMaskActive
		if wire.request.Active != active || wire.request.NullMask != wantMask {
			t.Fatalf("SetActive changed other fields: %+v", wire.request)
		}
		if g.RevisedUpdateRate() != 600*time.Millisecond || persistent.rate != 600 {
			t.Fatal("SetActive changed the revised update rate")
		}
	}
}
