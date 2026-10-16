package dump

import (
	"bytes"
	"compress/gzip"
	"context"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jufianto/sevjala/dump/dumptest"
)

func TestValidName(t *testing.T) {
	for name, want := range map[string]bool{
		"shop": true, "shop_restored_20261014_0030": true, "a$b-c": true, "X9": true,
		"": false, "a b": false, "a`b": false, "a'b": false, "a.b": false, "a/b": false, `a\b`: false,
		strings.Repeat("x", 64): true, strings.Repeat("x", 65): false,
	} {
		if got := ValidName(name); got != want {
			t.Errorf("ValidName(%q) = %v", name, got)
		}
	}
}

func TestDefaultTarget(t *testing.T) {
	at := time.Date(2026, 10, 14, 0, 30, 59, 0, time.Local)
	if got := DefaultTarget("otp", at); got != "otp_restored_20261014_0030" {
		t.Errorf("got %s", got)
	}
	long := DefaultTarget(strings.Repeat("d", 64), at)
	if len(long) != 64 || !strings.HasSuffix(long, "_restored_20261014_0030") || !ValidName(long) {
		t.Errorf("long name %q (%d)", long, len(long))
	}
}

func TestSchemaCreateDropCount(t *testing.T) {
	tools, log := fakeTools(t, dumptest.OK)
	ctx := context.Background()
	cs, coll, ok, err := Schema(ctx, tools, conn, "shop")
	if err != nil || !ok || cs != "utf8mb4" || coll != "utf8mb4_general_ci" {
		t.Errorf("shop: %q %q %v %v", cs, coll, ok, err)
	}
	if _, _, ok, err := Schema(ctx, tools, conn, "nope"); ok || err != nil {
		t.Errorf("nope exists=%v err=%v", ok, err)
	}
	if err := CreateDatabase(ctx, tools, conn, "shop_r", "utf8mb4", "utf8mb4_general_ci"); err != nil {
		t.Fatal(err)
	}
	if err := CreateDatabase(ctx, tools, conn, "plain", "", ""); err != nil {
		t.Fatal(err)
	}
	if err := DropDatabase(ctx, tools, conn, "shop_r"); err != nil {
		t.Fatal(err)
	}
	if n, err := TableCount(ctx, tools, conn, "shop_r"); err != nil || n != 2 {
		t.Errorf("tables %d, %v", n, err)
	}
	all := strings.Join(calls(t, log), "\n")
	for _, want := range []string{"CREATE DATABASE `shop_r` CHARACTER SET utf8mb4 COLLATE utf8mb4_general_ci",
		"CREATE DATABASE `plain` MYSQL_PWD", "DROP DATABASE `shop_r`"} {
		if !strings.Contains(all, want) {
			t.Errorf("no %q in\n%s", want, all)
		}
	}
	for _, bad := range []struct{ name, cs string }{{"a`b", ""}, {"ok", "utf8; DROP"}} {
		if err := CreateDatabase(ctx, tools, conn, bad.name, bad.cs, ""); err == nil {
			t.Errorf("CreateDatabase(%q, %q) ran", bad.name, bad.cs)
		}
	}
}

func gz(t *testing.T, s string) *bytes.Reader {
	t.Helper()
	var b bytes.Buffer
	w := gzip.NewWriter(&b)
	w.Write([]byte(s))
	w.Close()
	return bytes.NewReader(b.Bytes())
}

func TestLoad(t *testing.T) {
	tools, log := fakeTools(t, dumptest.OK)
	dumpSQL := dumptest.Dump("shop")
	if err := Load(context.Background(), tools, conn, "shop_r", gz(t, dumpSQL)); err != nil {
		t.Fatal(err)
	}
	all := strings.Join(calls(t, log), "\n")
	if !strings.Contains(all, "--default-character-set=utf8mb4 shop_r MYSQL_PWD="+pw) ||
		!strings.Contains(all, "LOAD shop_r "+strconv.Itoa(len(dumpSQL))) {
		t.Errorf("calls:\n%s", all)
	}

	tools, _ = fakeTools(t, dumptest.LoadFails)
	err := Load(context.Background(), tools, conn, "shop_r", gz(t, dumpSQL))
	if err == nil || strings.Contains(err.Error(), pw) || !strings.Contains(err.Error(), "Duplicate entry") {
		t.Errorf("failed load: %v", err)
	}
	if err := Load(context.Background(), tools, conn, "shop_r", strings.NewReader("not gzip")); err == nil {
		t.Error("a file that is not gzip must fail")
	}
}
