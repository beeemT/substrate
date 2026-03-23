package sqlite

import (
	"github.com/beeemT/go-atomic"
	"github.com/beeemT/go-atomic/generic"
	goatomicsqlx "github.com/beeemT/go-atomic/generic/sqlx"
	"github.com/beeemT/substrate/internal/repository"
	"github.com/jmoiron/sqlx"
)

// NewTransacter creates a transacter backed by the given DB.
func NewTransacter(db *sqlx.DB) atomic.Transacter[repository.Resources] {
	executer := goatomicsqlx.NewExecuter(db)
	return generic.NewTransacter[generic.SQLXRemote, repository.Resources](executer, ResourcesFactory)
}
