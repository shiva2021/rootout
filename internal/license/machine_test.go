package license

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"reflect"
	"testing"
)

const ioregSample = `+-o Root  <class IORegistryEntry, id 0x100000100, retain 31>
  +-o J314sAP  <class IOPlatformExpertDevice, id 0x10000020f, registered, matched, active, busy 0 (60 ms), retain 39>
    {
      "IOPlatformSerialNumber" = "C02XXXXXXXXX"
      "IOPlatformUUID" = "7a3f1c2e-1234-5678-9abc-def012345678"
      "manufacturer" = <"Apple Inc.">
    }
`

func TestMachineIDFromIoreg(t *testing.T) {
	r := func(_ context.Context, name string, args ...string) ([]byte, error) {
		if want := []string{"ioreg", "-rd1", "-c", "IOPlatformExpertDevice"}; !reflect.DeepEqual(append([]string{name}, args...), want) {
			t.Errorf("argv = %v, want %v", append([]string{name}, args...), want)
		}
		return []byte(ioregSample), nil
	}
	got, err := MachineID(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte("rootout:7A3F1C2E-1234-5678-9ABC-DEF012345678"))
	if want := hex.EncodeToString(sum[:])[:32]; got != want {
		t.Errorf("got %s, want %s", got, want)
	}
}

func TestMachineIDMissingUUID(t *testing.T) {
	r := func(context.Context, string, ...string) ([]byte, error) { return []byte("{ \"other\" = \"x\" }"), nil }
	if _, err := MachineID(context.Background(), r); !errors.Is(err, ErrNoMachineID) {
		t.Errorf("err = %v, want ErrNoMachineID", err)
	}
}

func TestMachineIDRunnerError(t *testing.T) {
	boom := errors.New("boom")
	r := func(context.Context, string, ...string) ([]byte, error) { return nil, boom }
	_, err := MachineID(context.Background(), r)
	if !errors.Is(err, ErrNoMachineID) || !errors.Is(err, boom) {
		t.Errorf("err = %v, want ErrNoMachineID wrapping boom", err)
	}
}
