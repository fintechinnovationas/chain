package main

import (
	"crypto/hmac"
	"crypto/sha512"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/sha3"
	"golang.org/x/net/context"

	"github.com/davecgh/go-spew/spew"

	"chain/cos/bc"
	"chain/cos/txscript"
	"chain/crypto/ed25519"
	"chain/crypto/ed25519/hd25519"
)

// A timed reader times out its Read() operation after a specified
// time limit.  We use it to wrap os.Stdin in case the user
// unwittingly supplies too few arguments and we block trying to read
// stdin from the terminal.
type timedReader struct {
	io.Reader
	limit time.Duration
}

func (r timedReader) Read(buf []byte) (int, error) {
	ctx, cancel := context.WithTimeout(context.Background(), r.limit)
	defer cancel()
	type readResult struct {
		n   int
		err error
	}
	readRes := make(chan readResult)
	go func() {
		n, err := r.Reader.Read(buf)
		readRes <- readResult{n, err}
		close(readRes)
	}()
	for {
		select {
		case <-ctx.Done():
			return 0, ctx.Err()
		case res := <-readRes:
			return res.n, res.err
		}
	}
}

var stdin = timedReader{
	Reader: os.Stdin,
	limit:  5 * time.Second,
}

type command struct {
	fn          func([]string)
	help, usage string
}

var subcommands = map[string]command{
	"assetid":     command{assetid, "compute asset id", "ISSUANCEPROG GENESISHASH"},
	"block":       command{block, "decode and pretty-print a block", "BLOCK"},
	"blockheader": command{blockheader, "decode and pretty-print a block header", "BLOCKHEADER"},
	"derive":      command{derive, "derive child from given xpub or xprv and given path", "XPUB/XPRV PATH PATH..."},
	"genprv":      command{genprv, "generate prv", ""},
	"genxprv":     command{genxprv, "generate xprv", ""},
	"hex":         command{hexCmd, "string <-> hex", "INPUT"},
	"hmac512":     command{hmac512, "compute the hmac512 digest", "KEY VALUE"},
	"pub":         command{pub, "get pub key from prv, or xpub from xprv", "PRV/XPRV"},
	"script":      command{script, "hex <-> opcodes", "INPUT"},
	"sha3":        command{sha3Cmd, "produce sha3 hash", "INPUT"},
	"sign":        command{sign, "sign, using hex PRV or XPRV, the given hex MSG", "PRV/XPRV MSG"},
	"tx":          command{tx, "decode and pretty-print a transaction", "TX"},
	"uvarint":     command{uvarint, "decimal <-> hex", "[-from|-to] VAL"},
	"varint":      command{varint, "decimal <-> hex", "[-from|-to] VAL"},
	"verify":      command{verify, "verify, using hex PUB or XPUB and the given hex MSG and SIG", "PUB/XPUB MSG SIG"},
	"zerohash":    command{zerohash, "produce an all-zeroes hash", ""},
}

func init() {
	// This breaks an initialization loop
	subcommands["help"] = command{help, "show help", "[SUBCOMMAND]"}
}

func main() {
	if len(os.Args) < 2 {
		errorf("no subcommand (try \"%s help\")", os.Args[0])
	}
	subcommand := mustSubcommand(os.Args[1])
	subcommand.fn(os.Args[2:])
}

func errorf(msg string, args ...interface{}) {
	fmt.Println(fmt.Sprintf(msg, args...))
	os.Exit(1)
}

func help(args []string) {
	if len(args) > 0 {
		subcommand := mustSubcommand(args[0])
		fmt.Println(subcommand.help)
		fmt.Printf("%s %s\n", args[0], subcommand.usage)
		return
	}

	for name, cmd := range subcommands {
		fmt.Printf("%-16.16s %s\n", name, cmd.help)
	}
}

func mustSubcommand(name string) command {
	if cmd, ok := subcommands[name]; ok {
		return cmd
	}
	errorf("unknown subcommand \"%s\"", name)
	return command{} // not reached
}

func input(args []string, n int, usedStdin bool) (string, bool) {
	if len(args) > n && args[n] != "-" {
		return args[n], usedStdin
	}
	if usedStdin {
		errorf("can use stdin for only one arg")
	}
	b, err := ioutil.ReadAll(stdin)
	if err != nil {
		errorf("unexpected error: %s", err)
	}
	return string(b), true
}

func mustDecodeHex(s string) []byte {
	res, err := hex.DecodeString(s)
	if err != nil {
		errorf("error decoding hex: %s", err)
	}
	return res
}

func mustDecodeHash(s string) bc.Hash {
	var h bc.Hash
	err := h.UnmarshalText([]byte(s))
	if err != nil {
		errorf("error decoding hash: %s", err)
	}
	return h
}

func assetid(args []string) {
	var (
		issuanceInp, genesisInp string
		usedStdin               bool
	)
	issuanceInp, usedStdin = input(args, 0, false)
	genesisInp, _ = input(args, 1, usedStdin)
	issuance := mustDecodeHex(issuanceInp)
	genesis := mustDecodeHash(genesisInp)
	assetID := bc.ComputeAssetID(issuance, genesis, 1)
	fmt.Println(assetID.String())
}

func block(args []string) {
	inp, _ := input(args, 0, false)
	var block bc.Block
	err := json.Unmarshal([]byte(inp), &block)
	if err != nil {
		errorf("error unmarshaling block: %s", err)
	}
	spew.Printf("%v\n", block)
}

func blockheader(args []string) {
	inp, _ := input(args, 0, false)
	var bh bc.BlockHeader
	err := json.Unmarshal([]byte(inp), &bh)
	if err != nil {
		errorf("error unmarshaling blockheader: %s", err)
	}
	spew.Printf("%v\n", bh)
}

func derive(args []string) {
	k, _ := input(args, 0, false)
	path := make([]uint32, 0, len(args)-1)
	for _, a := range args[1:] {
		p, err := strconv.ParseUint(a, 10, 32)
		if err != nil {
			errorf("could not parse %s as uint32", a)
		}
		path = append(path, uint32(p))
	}
	// XPrvs are longer than XPubs, try parsing one of those first.
	xprv, err := hd25519.XPrvFromString(k)
	if err == nil {
		derived := xprv.Derive(path)
		fmt.Println(derived.String())
		return
	}
	xpub, err := hd25519.XPubFromString(k)
	if err == nil {
		derived := xpub.Derive(path)
		fmt.Println(derived.String())
		return
	}
	errorf("could not parse key")
}

func genprv(_ []string) {
	_, prv, err := ed25519.GenerateKey(nil)
	if err != nil {
		errorf("unexpected error %s", err)
	}
	fmt.Println(hex.EncodeToString(hd25519.PrvBytes(prv)))
}

func genxprv(_ []string) {
	xprv, _, err := hd25519.NewXKeys(nil)
	if err != nil {
		errorf("unexpected error %s", err)
	}
	fmt.Println(xprv.String())
}

func hexCmd(args []string) {
	inp, _ := input(args, 0, false)
	b, err := hex.DecodeString(inp)
	if err == nil {
		fmt.Println(string(b))
	} else {
		fmt.Println(hex.EncodeToString([]byte(inp)))
	}
}

func hmac512(args []string) {
	key, usedStdin := input(args, 0, false)
	val, _ := input(args, 1, usedStdin)
	mac := hmac.New(sha512.New, mustDecodeHex(key))
	mac.Write(mustDecodeHex(val))
	fmt.Println(hex.EncodeToString(mac.Sum(nil)))
}

func pub(args []string) {
	inp, _ := input(args, 0, false)
	xprv, err := hd25519.XPrvFromString(inp)
	if err == nil {
		fmt.Println(xprv.Public().String())
		return
	}
	prv, err := hd25519.PrvFromBytes(mustDecodeHex(inp))
	if err == nil {
		fmt.Println(hex.EncodeToString(hd25519.PubBytes(prv.Public().(ed25519.PublicKey))))
		return
	}
	errorf("could not parse key")
}

func script(args []string) {
	inp, _ := input(args, 0, false)
	b, err := hex.DecodeString(inp)
	if err == nil {
		dis, err := txscript.DisasmString(b)
		if err == nil {
			fmt.Println(dis)
			return
		}
		// The input parsed as hex but not as a compiled program. Maybe
		// it's an uncompiled program that just looks like hex. Fall
		// through and try it that way.
	}
	parsed, err := txscript.ParseScriptString(inp)
	if err == nil {
		fmt.Println(hex.EncodeToString(parsed))
		return
	}
	errorf("could not parse input")
}

func sha3Cmd(args []string) {
	inp, _ := input(args, 0, false)
	b := mustDecodeHex(inp)
	h := sha3.Sum256(b)
	fmt.Println(hex.EncodeToString(h[:]))
}

func sign(args []string) {
	var (
		keyInp, msgInp string
		usedStdin      bool
	)
	keyInp, usedStdin = input(args, 0, false)
	msgInp, _ = input(args, 1, usedStdin)
	var (
		xprv *hd25519.XPrv
		prv  ed25519.PrivateKey
		err  error
	)
	xprv, err = hd25519.XPrvFromString(keyInp)
	if err != nil {
		xprv = nil
		prv, err = hd25519.PrvFromBytes(mustDecodeHex(keyInp))
		if err != nil {
			errorf("could not parse key")
		}
	}
	msg := mustDecodeHex(msgInp)
	var signed []byte
	if xprv != nil {
		signed = xprv.Sign(msg)
	} else {
		signed = ed25519.Sign(prv, msg)
	}
	fmt.Println(hex.EncodeToString(signed))
}

func tx(args []string) {
	inp, _ := input(args, 0, false)
	var tx bc.TxData
	err := tx.UnmarshalText([]byte(inp))
	if err != nil {
		errorf("error unmarshaling tx: %s", err)
	}
	spew.Printf("%v\n", tx)
}

func varint(args []string) {
	dovarint(args, true)
}

func uvarint(args []string) {
	dovarint(args, false)
}

func dovarint(args []string, signed bool) {
	var mode string
	if len(args) > 0 {
		switch args[0] {
		case "-from", "-to":
			mode = args[0]
			args = args[1:]
		}
	}
	val, _ := input(args, 0, false)
	if mode == "" {
		if strings.HasPrefix(val, "0x") {
			mode = "-from"
			val = strings.TrimPrefix(val, "0x")
		} else {
			_, err := strconv.ParseInt(val, 10, 64)
			if err == nil {
				mode = "-to"
			} else {
				mode = "-from"
			}
		}
	}
	switch mode {
	case "-to":
		val10, err := strconv.ParseInt(val, 10, 64)
		if err != nil {
			errorf("could not parse base 10 int")
		}
		var (
			buf [10]byte
			n   int
		)
		if signed {
			n = binary.PutVarint(buf[:], val10)
		} else {
			n = binary.PutUvarint(buf[:], uint64(val10))
		}
		fmt.Println(hex.EncodeToString(buf[:n]))
	case "-from":
		val16 := mustDecodeHex(val)
		if signed {
			n, nbytes := binary.Varint(val16)
			if nbytes <= 0 {
				errorf("could not parse varint")
			}
			fmt.Println(n)
		} else {
			n, nbytes := binary.Uvarint(val16)
			if nbytes <= 0 {
				errorf("could not parse varint")
			}
			fmt.Println(n)
		}
	}
}

func verify(args []string) {
	var (
		keyInp, msgInp, sigInp string
		usedStdin              bool
	)
	keyInp, usedStdin = input(args, 0, false)
	msgInp, usedStdin = input(args, 1, usedStdin)
	sigInp, _ = input(args, 2, usedStdin)
	var (
		xpub *hd25519.XPub
		pub  ed25519.PublicKey
		err  error
	)
	xpub, err = hd25519.XPubFromString(keyInp)
	if err != nil {
		xpub = nil
		pub, err = hd25519.PubFromBytes(mustDecodeHex(keyInp))
		if err != nil {
			errorf("could not parse key")
		}
	}
	msg := mustDecodeHex(msgInp)
	sig := mustDecodeHex(sigInp)
	var verified bool
	if xpub != nil {
		verified = xpub.Verify(msg, sig)
	} else {
		verified = ed25519.Verify(pub, msg, sig)
	}
	if verified {
		fmt.Println("verified")
	} else {
		fmt.Println("not verified")
	}
}

func zerohash(_ []string) {
	fmt.Println(bc.Hash{}.String())
}
