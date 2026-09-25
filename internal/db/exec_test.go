package db

import "testing"

func TestStatementWhitelist(t *testing.T) {
	ok, err := Statement(Request{Action: "create_database", Database: "appdb"})
	if err != nil || ok != "CREATE DATABASE appdb" {
		t.Fatalf("建库 = %q %v", ok, err)
	}
	if _, err := Statement(Request{Action: "create_database", Database: "xuntai"}); err != ErrSQL {
		t.Fatalf("xuntai = %v", err)
	}
	if _, err := Statement(Request{Action: "drop_database", Database: "appdb"}); err != ErrWhitelist {
		t.Fatalf("动作 = %v", err)
	}
	account, err := Statement(Request{Action: "create_account", Account: "app", AccountHost: "10.8.2.17"})
	if err != nil || account != "CREATE USER 'app'@'10.8.2.17'" {
		t.Fatalf("建账号 = %q %v", account, err)
	}
	sql, err := Statement(Request{Action: "sql_change", SQL: "ALTER TABLE orders ADD COLUMN note varchar(32)"})
	if err != nil || sql == "" {
		t.Fatalf("改表 = %q %v", sql, err)
	}
	for _, raw := range []string{
		"DROP TABLE orders",
		"DELETE FROM orders",
		"ALTER TABLE orders; DROP TABLE orders",
		"CREATE TABLE t (id int) -- hidden",
		"UPDATE orders SET id = 1",
	} {
		if _, err := Statement(Request{Action: "sql_change", SQL: raw}); err != ErrSQL {
			t.Fatalf("%s = %v", raw, err)
		}
	}
}
