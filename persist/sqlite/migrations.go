package sqlite

import (
	"context"

	"go.uber.org/zap"
)

// migrations is a list of functions that are run to migrate the database from
// one version to the next. Migrations are used to update existing databases to
// match the schema in init.sql.
var migrations = []func(ctx context.Context, tx *txn, log *zap.Logger) error{}
