// Package services lists all mocked STACKIT services.
package services

import (
	"net/http"

	"github.com/morks/stackit_mock/internal/httpx"
	"github.com/morks/stackit_mock/internal/services/auth"
	"github.com/morks/stackit_mock/internal/services/iaas"
	"github.com/morks/stackit_mock/internal/services/mongodbflex"
	"github.com/morks/stackit_mock/internal/services/postgresflex"
	"github.com/morks/stackit_mock/internal/services/resourcemanager"
	"github.com/morks/stackit_mock/internal/services/serviceaccount"
	"github.com/morks/stackit_mock/internal/services/ske"
)

type Registrar = func(mux *http.ServeMux, d *httpx.Deps)

func All() []Registrar {
	return []Registrar{
		auth.Register,
		resourcemanager.Register,
		serviceaccount.Register,
		iaas.Register,
		ske.Register,
		postgresflex.Register,
		mongodbflex.Register,
	}
}
