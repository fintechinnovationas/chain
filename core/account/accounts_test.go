package account

import (
	"bytes"
	"testing"

	"golang.org/x/net/context"

	"chain/core/signers"
	"chain/cos/txscript"
	"chain/database/pg"
	"chain/database/pg/pgtest"
	"chain/testutil"
)

var dummyXPub = testutil.TestXPub.String()

func resetSeqs(ctx context.Context, t testing.TB) {
	acpIndexNext, acpIndexCap = 1, 100
	pgtest.Exec(ctx, t, `ALTER SEQUENCE account_control_program_seq RESTART`)
	pgtest.Exec(ctx, t, `ALTER SEQUENCE signers_key_index_seq RESTART`)
}

func TestCreateControlProgram(t *testing.T) {
	ctx := pg.NewContext(context.Background(), pgtest.NewTx(t))
	resetSeqs(ctx, t)

	account, err := Create(ctx, []string{dummyXPub}, 1, nil, nil)
	if err != nil {
		testutil.FatalErr(t, err)
	}

	got, err := CreateControlProgram(ctx, account.ID)
	if err != nil {
		testutil.FatalErr(t, err)
	}

	want, err := txscript.ParseScriptString("OP_DUP OP_SHA3 OP_DATA_32 0x963e9956eabe4610b042a3b085a1dc648e7fd87298b51d0369e2f66446338739 OP_EQUALVERIFY 0 OP_CHECKPREDICATE")
	if err != nil {
		testutil.FatalErr(t, err)
	}

	if !bytes.Equal(got, want) {
		t.Errorf("got control program = %x want %x", got, want)
	}
}

func createTestAccount(ctx context.Context, t testing.TB, tags map[string]interface{}) *signers.Signer {
	account, err := Create(ctx, []string{dummyXPub}, 1, tags, nil)
	if err != nil {
		testutil.FatalErr(t, err)
	}

	return account
}

func createTestControlProgram(ctx context.Context, t testing.TB, accountID string) []byte {
	if accountID == "" {
		account := createTestAccount(ctx, t, nil)
		accountID = account.ID
	}

	acp, err := CreateControlProgram(ctx, accountID)
	if err != nil {
		testutil.FatalErr(t, err)
	}
	return acp
}
