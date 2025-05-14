package helm_client

import (
	"context"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gofrs/flock"
	"github.com/pkg/errors"
	"gopkg.in/yaml.v2"
	"helm.sh/helm/v3/pkg/getter"
	"helm.sh/helm/v3/pkg/repo"
)

type RepoAddOptions struct {
	Name               string
	URL                string
	Username           string // --username string
	Password           string // --password string
	PassCredentialsAll bool   // --pass-credentials

	CertFile              string // --cert-file string
	KeyFile               string // --key-file string
	CaFile                string // --ca-file string
	InsecureSkipTLSverify bool   // --insecure-skip-tls-verify

	NoRepoExsitsError bool // When set to true, no error will be returned when the same repo exists.
	UpdateWhenExsits  bool // --force-update
}

func (cli *baseClient) repoAdd(o *RepoAddOptions) error {
	repoFile := cli.settings.RepositoryConfig
	repoCache := cli.settings.RepositoryCache

	// create repo file
	err := os.MkdirAll(filepath.Dir(repoFile), os.ModePerm)
	if err != nil && !os.IsExist(err) {
		return err
	}

	// lock repo file
	repoFileExt := filepath.Ext(repoFile)
	var lockPath string
	if len(repoFileExt) > 0 && len(repoFileExt) < len(repoFile) {
		lockPath = strings.TrimSuffix(repoFile, repoFileExt) + ".lock"
	} else {
		lockPath = repoFile + ".lock"
	}
	fileLock := flock.New(lockPath)
	lockCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	locked, err := fileLock.TryLockContext(lockCtx, time.Second)
	if err == nil && locked {
		defer func() {
			if err := fileLock.Unlock(); err != nil {
				log.Printf("Failed to unlock file: %v", err)
			}
		}()
	}
	if err != nil {
		return err
	}

	// read repo file
	b, err := os.ReadFile(repoFile)
	if err != nil && !os.IsNotExist(err) {
		return err
	}

	var f repo.File
	if err := yaml.Unmarshal(b, &f); err != nil {
		return err
	}

	c := repo.Entry{
		Name:                  o.Name,
		URL:                   o.URL,
		Username:              o.Username,
		Password:              o.Password,
		PassCredentialsAll:    o.PassCredentialsAll,
		CertFile:              o.CertFile,
		KeyFile:               o.KeyFile,
		CAFile:                o.CaFile,
		InsecureSkipTLSverify: o.InsecureSkipTLSverify,
	}

	if strings.Contains(o.Name, "/") {
		return errors.Errorf("repository name (%s) contains '/', please specify a different name without '/'", o.Name)
	}

	// There is a repo with the same name
	if f.Has(o.Name) {
		existing := f.Get(o.Name)
		if c != *existing {
			return errors.Errorf("repository name (%s) already exists, please specify a different name", o.Name)
		}

		if o.UpdateWhenExsits {
			if err := cli.repoUpdate(&RepoUpdateOptions{Names: []string{o.Name}}); err != nil {
				log.Printf("Failed to update repository: %v", err)
			}
		}

		if o.NoRepoExsitsError {
			return nil
		} else {
			return errors.Errorf("%q already exists with the same configuration, skipping\n", o.Name)
		}
	}

	// Add new repo configuration in repo file
	r, err := repo.NewChartRepository(&c, getter.All(cli.settings))
	if err != nil {
		return err
	}

	if repoCache != "" {
		r.CachePath = repoCache
	}
	if _, err := r.DownloadIndexFile(); err != nil {
		return errors.Wrapf(err, "looks like %q is not a valid chart repository or cannot be reached", o.URL)
	}

	f.Update(&c)

	if err := f.WriteFile(repoFile, 0644); err != nil {
		return err
	}
	return nil
}
