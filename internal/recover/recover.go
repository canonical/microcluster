package recover

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"strings"

	dqlite "github.com/canonical/go-dqlite/client"
	"github.com/canonical/lxd/shared/logger"
	"gopkg.in/yaml.v2"

	"github.com/canonical/microcluster/cluster"
	"github.com/canonical/microcluster/internal/sys"
	"github.com/canonical/microcluster/internal/trust"
)

// GetLocalClusterMembers parses the trust store and
// path.Join(filesystem.DatabaseDir, "cluster.yaml").
func GetLocalClusterMembers(filesystem *sys.OS) ([]cluster.LocalMember, error) {
	storePath := path.Join(filesystem.DatabaseDir, "cluster.yaml")
	nodeInfo, err := dumpYamlNodeStore(storePath)
	if err != nil {
		return nil, err
	}

	remotes, err := ReadTrustStore(filesystem.TrustDir)
	if err != nil {
		return nil, err
	}

	remotesByName := remotes.RemotesByName()

	var members []cluster.LocalMember
	for _, remote := range remotesByName {
		for _, info := range nodeInfo {
			if remote.Address.String() == info.Address {
				members = append(members, cluster.LocalMember{
					DqliteID: info.ID,
					Address:  info.Address,
					Role:     info.Role.String(),
					Name:     remote.Name,
				})
			}
		}
	}

	return members, nil
}

func dumpYamlNodeStore(path string) ([]dqlite.NodeInfo, error) {
	store, err := dqlite.NewYamlNodeStore(path)
	if err != nil {
		return nil, fmt.Errorf("Failed to read %q: %w", path, err)
	}

	nodeInfo, err := store.Get(context.Background())
	if err != nil {
		return nil, fmt.Errorf("Failed to read from node store: %w", err)
	}

	return nodeInfo, nil
}

// ReadTrustStore parses the trust store. This is not thread safe!
func ReadTrustStore(dir string) (*trust.Remotes, error) {
	remotes := &trust.Remotes{}
	err := remotes.Load(dir)

	return remotes, err
}

// ValidateMemberChanges compares two arrays of members to ensure:
// - Their lengths are the same.
// - Members with the same name also use the same ID and address.
func ValidateMemberChanges(oldMembers []cluster.LocalMember, newMembers []cluster.LocalMember) error {
	if len(newMembers) != len(oldMembers) {
		return fmt.Errorf("members cannot be added or removed")
	}

	for _, newMember := range newMembers {
		memberValid := false
		for _, oldMember := range oldMembers {
			// FIXME: Allow changing member addresses as part of cluster recovery
			membersMatch := newMember.DqliteID == oldMember.DqliteID &&
				newMember.Name == oldMember.Name &&
				newMember.Address == oldMember.Address

			if membersMatch {
				memberValid = true
				break
			}
		}

		if !memberValid {
			return fmt.Errorf("ID or address changed for member %s", newMember.Name)
		}
	}

	return nil
}

// CreateRecoveryTarball writes a tarball of filesystem.DatabaseDir to
// filesystem.StateDir.
// go-dqlite's info.yaml is excluded from the tarball.
func CreateRecoveryTarball(filesystem *sys.OS) error {
	dbFS := os.DirFS(filesystem.DatabaseDir)
	dbFiles, err := fs.Glob(dbFS, "*")
	if err != nil {
		return fmt.Errorf("%w", err)
	}

	tarballPath := path.Join(filesystem.StateDir, "recovery_db.tar.gz")

	// info.yaml is used by go-dqlite to keep track of the current cluster member's
	// ID and address. We shouldn't replicate the recovery member's info.yaml
	// to all other members, so exclude it from the tarball:
	for indx, filename := range dbFiles {
		if filename == "info.yaml" {
			newlen := len(dbFiles) - 1
			dbFiles[indx] = dbFiles[newlen]
			dbFiles = dbFiles[:newlen]
			break
		}
	}

	return createTarball(tarballPath, filesystem.DatabaseDir, dbFiles)
}

// MaybeUnpackRecoveryTarball checks for the presence of a recovery tarball in
// fiesystem.StateDir. If it exists, unpack it into a temporary directory,
// ensure that it is a valid microcluster recovery tarball, and replace the
// existing filesystem.DatabaseDir.
func MaybeUnpackRecoveryTarball(filesystem *sys.OS) error {
	tarballPath := path.Join(filesystem.StateDir, "recovery_db.tar.gz")
	unpackDir := path.Join(filesystem.StateDir, "recovery_db")

	// Determine if the recovery tarball exists
	if _, err := os.Stat(tarballPath); errors.Is(err, os.ErrNotExist) {
		return nil
	}

	logger.Warn("Recovery tarball located; attempting DB recovery", logger.Ctx{"tarball": tarballPath})

	err := unpackTarball(tarballPath, unpackDir)
	if err != nil {
		return err
	}

	// TODO Is this a reasonable sanity check or is a parseable cluster.yaml
	// containing the local node sufficient?
	metadataPath := path.Join(unpackDir, "metadata1")
	if _, err := os.Stat(metadataPath); errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("missing %q in recovery tarball", metadataPath)
	}

	// sanity check: valid cluster.yaml in the incoming DB dir
	clusterYamlPath := path.Join(unpackDir, "cluster.yaml")
	incomingNodeInfo, err := dumpYamlNodeStore(clusterYamlPath)
	if err != nil {
		return err
	}

	// use the local info.yaml so that the dqlite ID is preserved on each
	// cluster member
	localInfoYamlPath := path.Join(filesystem.DatabaseDir, "info.yaml")
	recoveryInfoYamlPath := path.Join(unpackDir, "info.yaml")

	localInfoYaml, err := os.ReadFile(localInfoYamlPath)
	if err != nil {
		return err
	}

	var localInfo dqlite.NodeInfo
	err = yaml.Unmarshal(localInfoYaml, &localInfo)
	if err != nil {
		return fmt.Errorf("invalid %q", localInfoYamlPath)
	}

	found := false
	for _, incomingInfo := range incomingNodeInfo {
		found = localInfo.ID == incomingInfo.ID

		if found {
			break
		}
	}

	if !found {
		return fmt.Errorf("missing local cluster member in incoming cluster.yaml")
	}

	err = os.WriteFile(recoveryInfoYamlPath, localInfoYaml, 0o664)
	if err != nil {
		return err
	}

	// FIXME: Take a DB backup

	// Now that we're as sure as we can be that the recovery DB is valid, we can
	// replace the existing DB
	err = os.RemoveAll(filesystem.DatabaseDir)
	if err != nil {
		return err
	}

	err = os.Rename(unpackDir, filesystem.DatabaseDir)
	if err != nil {
		return err
	}

	// Prevent the database being restored again after subsequent restarts
	err = os.Remove(tarballPath)
	if err != nil {
		return err
	}

	return nil
}

// create tarball at tarballPath with files path.Join(dir, file)
// Note: does not handle subdirectories.
func createTarball(tarballPath string, dir string, files []string) error {
	tarball, err := os.Create(tarballPath)
	if err != nil {
		return err
	}

	gzWriter := gzip.NewWriter(tarball)
	tarWriter := tar.NewWriter(gzWriter)

	for _, filename := range files {
		filepath := path.Join(dir, filename)

		file, err := os.Open(filepath)
		if err != nil {
			return err
		}

		stat, err := file.Stat()
		if err != nil {
			return err
		}

		// Note: header.Name is set to the basename of stat. If dqlite starts
		// using subdirs in the DB dir, this will need modification
		header, err := tar.FileInfoHeader(stat, filename)
		if err != nil {
			return fmt.Errorf("create tar header for %q: %w", filepath, err)
		}

		err = tarWriter.WriteHeader(header)
		if err != nil {
			return err
		}

		_, err = io.Copy(tarWriter, file)
		if err != nil {
			return err
		}

		err = file.Close()
		if err != nil {
			return err
		}
	}

	err = tarWriter.Close()
	if err != nil {
		return err
	}

	err = gzWriter.Close()
	if err != nil {
		return err
	}

	err = tarball.Close()
	if err != nil {
		return err
	}

	return nil
}

// Note: Does not handle subdirectories.
func unpackTarball(tarballPath string, destRoot string) error {
	tarball, err := os.Open(tarballPath)
	if err != nil {
		return err
	}

	gzReader, err := gzip.NewReader(tarball)
	if err != nil {
		return err
	}

	tarReader := tar.NewReader(gzReader)

	err = os.MkdirAll(destRoot, 0o755)
	if err != nil {
		return err
	}

	for {
		header, err := tarReader.Next()
		if err == io.EOF {
			break
		} else if err != nil {
			return err
		}

		// CWE-22
		if strings.Contains(header.Name, "..") {
			return fmt.Errorf("Invalid sequence `..` in recovery tarball entry %q", header.Name)
		}

		filepath := path.Join(destRoot, header.Name)
		file, err := os.Create(filepath)
		if err != nil {
			return err
		}

		countWritten, err := io.Copy(file, tarReader)
		if countWritten != header.Size {
			return fmt.Errorf("mismatched written (%d) and size (%d) for entry %q in %q", countWritten, header.Size, header.Name, tarballPath)
		} else if err != nil {
			return err
		}
	}

	return nil
}
