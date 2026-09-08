package opcda

// OPC DA quality is a 16-bit WORD. From most to least significant bit:
//
//     VVVVVVVV QQ SSSS LL
//
// V (bits 8-15) is vendor-specific information; this library preserves it.
// Q (bits 6-7) is the primary class: 00 Bad, 01 Uncertain, 10 reserved,
// 11 Good. S (bits 2-5) identifies a sub-status within that class.
// L (bits 0-1) indicates a value limit: 00 not limited, 01 low limited,
// 10 high limited, 11 constant. Limit bits do not change the primary class.
//
// The standard class/sub-status codes below exclude vendor and limit bits:
//
//   0x00 Bad: no more specific reason is available.
//   0x04 Configuration error: the server cannot use the item's configuration.
//   0x08 Not connected: the item's data source is not connected.
//   0x0C Device failure: the data source reports a device failure.
//   0x10 Sensor failure: the data source reports a sensor failure.
//   0x14 Last known value: communication failed; the last known value remains.
//   0x18 Communication failure: communication failed without an available value.
//   0x1C Out of service: the item is not currently in service.
//   0x20 Waiting for initial value: no initial value has arrived yet.
//
//   0x40 Uncertain: the value's usability is uncertain, without further detail.
//   0x44 Last usable value: updates have stopped; the last value may be stale.
//   0x50 Sensor not accurate: the sensor's accuracy is not assured.
//   0x54 Engineering units exceeded: the value exceeds its engineering range.
//   0x58 Sub-normal: the value is derived from fewer than the required Good inputs.
//
//   0xC0 Good: the value has Good quality without a more specific sub-status.
//   0xD8 Local override: the value has been overridden locally.
//
// A Bad item does not by itself mean the client's DCOM session has failed:
// the server can successfully report a downstream device/sensor failure.
// Likewise, Good quality does not prove that a cache read sampled the device
// afresh. Consumers decide which classes to store or alert on.
//
// QualityDescription names the standard class/sub-status only; it does not
// decode vendor-specific meanings or append limit information. Unknown
// sub-statuses retain their primary class. Convert q to uint16 when displaying
// the full raw WORD, since the public read result currently stores it as int16.
//
// Reference: OPC DA Custom Interface specification, Quality field;
// OPC 10000-8, A.3.2.3:
// https://reference.opcfoundation.org/specs/OPC-10000-8/a-3-2-3

// QualityDescription describes the standard class and sub-status of an OPC DA
// quality WORD. Vendor bits (8-15) and limit bits (0-1) do not change this
// description; the caller retains the original quality for those details.
// The reserved primary class is not treated as Good, Bad, or Uncertain.
func QualityDescription(q int16) string {
	switch uint16(q) & 0xfc {
	case 0x00:
		return "Bad (non-specific)"
	case 0x04:
		return "Bad (configuration error)"
	case 0x08:
		return "Bad (not connected)"
	case 0x0c:
		return "Bad (device failure)"
	case 0x10:
		return "Bad (sensor failure)"
	case 0x14:
		return "Bad (last known value)"
	case 0x18:
		return "Bad (communication failure)"
	case 0x1c:
		return "Bad (out of service)"
	case 0x20:
		return "Bad (waiting for initial value)"
	case 0x44:
		return "Uncertain (last usable value)"
	case 0x50:
		return "Uncertain (sensor not accurate)"
	case 0x54:
		return "Uncertain (engineering units exceeded)"
	case 0x58:
		return "Uncertain (sub-normal)"
	case 0xd8:
		return "Good (local override)"
	}
	switch {
	case QualityIsGood(q):
		return "Good"
	case QualityIsBad(q):
		return "Bad"
	case QualityIsUncertain(q):
		return "Uncertain"
	default:
		return "Reserved quality"
	}
}
