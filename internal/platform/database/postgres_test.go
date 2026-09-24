package database

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"
)

type fakeChecker struct {
	pingErr error
	row     pgx.Row
}

func (checker fakeChecker) Ping(context.Context) error {
	return checker.pingErr
}

func (checker fakeChecker) QueryRow(context.Context, string, ...any) pgx.Row {
	return checker.row
}

type fakeRow struct {
	role    string
	version int
	err     error
}

func (row fakeRow) Scan(destinations ...any) error {
	if row.err != nil {
		return row.err
	}
	if len(destinations) == 1 {
		*destinations[0].(*bool) = true
		return nil
	}
	*destinations[0].(*string) = row.role
	*destinations[1].(*int) = row.version
	return nil
}

func TestCheckAcceptsExpectedRoleAndSchema(t *testing.T) {
	err := Check(context.Background(), fakeChecker{row: fakeRow{role: RoleAPI, version: SchemaVersion}}, RoleAPI)
	if err != nil {
		t.Fatalf("Check() error = %v", err)
	}
}

func TestCheckRejectsUnexpectedRole(t *testing.T) {
	err := Check(context.Background(), fakeChecker{row: fakeRow{role: RoleWorker, version: SchemaVersion}}, RoleAPI)
	if !errors.Is(err, ErrUnexpectedRole) {
		t.Fatalf("Check() error = %v, want ErrUnexpectedRole", err)
	}
}

func TestCheckRejectsIncompatibleSchema(t *testing.T) {
	err := Check(context.Background(), fakeChecker{row: fakeRow{role: RoleAPI, version: SchemaVersion + 1}}, RoleAPI)
	if !errors.Is(err, ErrIncompatibleSchema) {
		t.Fatalf("Check() error = %v, want ErrIncompatibleSchema", err)
	}
}

func TestCheckRejectsUnavailableDatabase(t *testing.T) {
	err := Check(context.Background(), fakeChecker{pingErr: errors.New("down")}, RoleAPI)
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("Check() error = %v, want ErrUnavailable", err)
	}
}
