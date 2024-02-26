package templates

import (
	"fmt"
	"io"

	"github.com/pkg/errors"
)

func (d deployer) FindTemplate(template string) ([]byte, error) {
	templ := fmt.Sprintf("/cnab/app/%s", template)
	f, err := d.context.FileSystem.Open(templ)
	if err != nil {
		return nil, errors.Wrap(err, fmt.Sprintf("couldn't find template %s", template))
	}
	defer f.Close()
	return io.ReadAll(f)
}
