package recover

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/netip"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/canonical/go-dqlite"
	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/logger"
	"gopkg.in/yaml.v3"

	"github.com/canonical/microcluster/client"
	"github.com/canonical/microcluster/cluster"
	"github.com/canonical/microcluster/internal/config"
	internalTypes "github.com/canonical/microcluster/internal/rest/types"
	"github.com/canonical/microcluster/internal/sys"
	"github.com/canonical/microcluster/internal/trust"
	"github.com/canonical/microcluster/rest/types"
)

// GetDqliteClusterMembers parses the trust store and
// path.Join(filesystem.DatabaseDir, "cluster.yaml").
func GetDqliteClusterMembers(filesystem *sys.OS) ([]cluster.DqliteMember, error) {
	storePath := path.Join(filesystem.DatabaseDir, "cluster.yaml")

	var nodeInfo []dqlite.NodeInfo
	err := readYaml(storePath, &nodeInfo)
	if err != nil {
		return nil, err
	}

	remotes, err := readTrustStore(filesystem.TrustDir)
	if err != nil {
		return nil, err
	}

	remotesByName := remotes.RemotesByName()

	var members []cluster.DqliteMember
	for _, remote := range remotesByName {
		for _, info := range nodeInfo {
			if remote.Address.String() == info.Address {
				members = append(members, cluster.DqliteMember{
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

// RecoverFromQuorumLoss resets the dqlite raft log, rewrites the go-dqlite yaml
// files, modifies the daemon and trust store, and writes a recovery tarball.
// It does not check members to ensure that the new configuration is valid; use
// ValidateMemberChanges to ensure that the inputs to this function are correct.
func RecoverFromQuorumLoss(filesystem *sys.OS, members []cluster.DqliteMember) (string, error) {
	// Set up our new cluster configuration
	nodeInfo := make([]dqlite.NodeInfo, 0, len(members))
	for _, member := range members {
		info, err := member.NodeInfo()
		if err != nil {
			return "", err
		}
		nodeInfo = append(nodeInfo, *info)
	}

	// Ensure that the daemon is not running
	isSocketPresent, err := filesystem.IsControlSocketPresent()
	if err != nil {
		return "", err
	}

	if isSocketPresent {
		return "", fmt.Errorf("Daemon is running (socket path exists: %q)", filesystem.ControlSocketPath())
	}

	// Check each cluster member's /1.0 to ensure that they are unreachable.
	// This is a sanity check to ensure that we're not reconfiguring a cluster
	// that's still partially up.
	remotes, err := readTrustStore(filesystem.TrustDir)
	if err != nil {
		return "", err
	}

	serverCert, err := filesystem.ServerCert()
	if err != nil {
		return "", err
	}

	clusterCert, err := filesystem.ClusterCert()
	if err != nil {
		return "", err
	}

	clusterKey, err := clusterCert.PublicKeyX509()
	if err != nil {
		return "", err
	}

	cluster, err := remotes.Cluster(false, serverCert, clusterKey)
	if err != nil {
		return "", err
	}

	cancelCtx, cancel := context.WithTimeout(context.Background(), time.Second*10)
	err = cluster.Query(cancelCtx, true, func(ctx context.Context, client *client.Client) error {
		var rslt internalTypes.Server
		err := client.Query(ctx, "GET", "1.0", api.NewURL(), nil, &rslt)
		if err == nil {
			return fmt.Errorf("Contacted cluster member at %q; please shut down all cluster members", rslt.Name)
		}
		return nil
	})
	cancel()
	if err != nil {
		return "", err
	}

	err = CreateDatabaseBackup(filesystem)
	if err != nil {
		return "", err
	}

	err = dqlite.ReconfigureMembershipExt(filesystem.DatabaseDir, nodeInfo)
	if err != nil {
		return "", fmt.Errorf("Dqlite recovery: %w", err)
	}

	err = writeDqliteClusterYaml(path.Join(filesystem.DatabaseDir, "cluster.yaml"), members)
	if err != nil {
		return "", err
	}

	// Tar up the m.FileSystem.DatabaseDir and write to `dbExportPath`
	recoveryTarballPath, err := createRecoveryTarball(filesystem, members)
	if err != nil {
		return "", err
	}

	err = updateTrustStore(filesystem.TrustDir, members)
	if err != nil {
		return recoveryTarballPath, fmt.Errorf("Failed to update trust store: %w", err)
	}

	err = writeGlobalMembersPatch(filesystem, members)
	if err != nil {
		return recoveryTarballPath, fmt.Errorf("Failed to write global DB update: %w", err)
	}

	return recoveryTarballPath, nil
}

func readYaml(path string, v any) error {
	yml, err := os.ReadFile(path)
	if err != nil {
		return err
	}

	err = yaml.Unmarshal(yml, v)
	if err != nil {
		return fmt.Errorf("Unmarshal %q: %w", path, err)
	}

	return nil
}

func writeYaml(path string, v any) error {
	yml, err := yaml.Marshal(v)
	if err != nil {
		return err
	}

	err = os.WriteFile(path, yml, os.FileMode(0o644))
	if err != nil {
		return err
	}

	return nil
}

func writeDqliteClusterYaml(path string, members []cluster.DqliteMember) error {
	nodeInfo := make([]dqlite.NodeInfo, len(members))
	for i, member := range members {
		infoPtr, err := member.NodeInfo()
		if err != nil {
			return err
		}
		nodeInfo[i] = *infoPtr
	}

	return writeYaml(path, &nodeInfo)
}

// ReadTrustStore parses the trust store. This is not thread safe!
func readTrustStore(dir string) (*trust.Remotes, error) {
	remotes := &trust.Remotes{}
	err := remotes.Load(dir)

	return remotes, err
}

// Update the trust store with the new member addresses.
func updateTrustStore(dir string, members []cluster.DqliteMember) error {
	remotes, err := readTrustStore(dir)
	if err != nil {
		return err
	}

	remotesByName := remotes.RemotesByName()

	trustMembers := make([]types.ClusterMember, 0, len(members))
	for _, member := range members {
		cert := remotesByName[member.Name].Certificate
		addr, err := netip.ParseAddrPort(member.Address)
		if err != nil {
			return fmt.Errorf("Invalid address %q: %w", member.Address, err)
		}

		trustMembers = append(trustMembers, types.ClusterMember{
			ClusterMemberLocal: types.ClusterMemberLocal{
				Name:        member.Name,
				Address:     types.AddrPort{AddrPort: addr},
				Certificate: cert,
			},
		})
	}

	err = remotes.Replace(dir, trustMembers...)
	if err != nil {
		return fmt.Errorf("Update trust store at %q: %w", dir, err)
	}

	return nil
}

// ValidateMemberChanges compares two arrays of members to ensure:
// - Their lengths are the same.
// - Members with the same name also use the same ID and address.
func ValidateMemberChanges(oldMembers []cluster.DqliteMember, newMembers []cluster.DqliteMember) error {
	if len(newMembers) != len(oldMembers) {
		return fmt.Errorf("members cannot be added or removed")
	}

	for _, newMember := range newMembers {
		memberValid := false
		for _, oldMember := range oldMembers {
			membersMatch := newMember.DqliteID == oldMember.DqliteID &&
				newMember.Name == oldMember.Name

			if membersMatch {
				memberValid = true
				break
			}
		}

		if !memberValid {
			return fmt.Errorf("ID or name changed for member %s", newMember.Name)
		}
	}

	return nil
}

func writeGlobalMembersPatch(filesystem *sys.OS, members []cluster.DqliteMember) error {
	sql := ""
	for _, member := range members {
		sql += fmt.Sprintf("UPDATE core_cluster_members SET address = %q WHERE name = %q;\n", member.Address, member.Name)
	}

	if len(sql) > 0 {
		patchPath := path.Join(filesystem.StateDir, "patch.global.sql")
		patchFile, err := os.OpenFile(patchPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
		if err != nil {
			return err
		}

		defer func() { _ = patchFile.Close() }()

		count, err := patchFile.Write([]byte(sql))
		if err != nil || len(sql) != count {
			return err
		}

		err = patchFile.Close()
		if err != nil {
			return err
		}
	}

	return nil
}

// CreateRecoveryTarball writes a tarball of filesystem.DatabaseDir to
// filesystem.StateDir.
// go-dqlite's info.yaml is excluded from the tarball.
// The new cluster configuration is included as `recovery.yaml`.
// This function returns the path to the tarball.
func createRecoveryTarball(filesystem *sys.OS, members []cluster.DqliteMember) (string, error) {
	tarballPath := path.Join(filesystem.StateDir, "recovery_db.tar.gz")
	recoveryYamlPath := path.Join(filesystem.DatabaseDir, "recovery.yaml")

	err := writeYaml(recoveryYamlPath, members)
	if err != nil {
		return "", err
	}

	// info.yaml is used by go-dqlite to keep track of the current cluster member's
	// ID and address. We shouldn't replicate the recovery member's info.yaml
	// to all other members, so exclude it from the tarball:
	err = createTarball(tarballPath, filesystem.DatabaseDir, ".", []string{"info.yaml"})

	return tarballPath, err
}

// MaybeUnpackRecoveryTarball checks for the presence of a recovery tarball in
// fiesystem.StateDir. If it exists, unpack it into a temporary directory,
// ensure that it is a valid microcluster recovery tarball, and replace the
// existing filesystem.DatabaseDir.
func MaybeUnpackRecoveryTarball(filesystem *sys.OS) error {
	tarballPath := path.Join(filesystem.StateDir, "recovery_db.tar.gz")
	unpackDir := path.Join(filesystem.StateDir, "recovery_db")
	recoveryYamlPath := path.Join(unpackDir, "recovery.yaml")

	// Determine if the recovery tarball exists
	if _, err := os.Stat(tarballPath); errors.Is(err, os.ErrNotExist) {
		return nil
	}

	logger.Warn("Recovery tarball located; attempting DB recovery", logger.Ctx{"tarball": tarballPath})

	err := unpackTarball(tarballPath, unpackDir)
	if err != nil {
		return err
	}

	// We need to set the local info.yaml address with the (possibly changed)
	// incoming address for this member.
	localInfoYamlPath := path.Join(filesystem.DatabaseDir, "info.yaml")
	recoveryInfoYamlPath := path.Join(unpackDir, "info.yaml")

	var localInfo dqlite.NodeInfo
	err = readYaml(localInfoYamlPath, &localInfo)
	if err != nil {
		return err
	}

	var incomingMembers []cluster.DqliteMember
	err = readYaml(recoveryYamlPath, &incomingMembers)
	if err != nil {
		return nil
	}

	found := false
	for _, incomingInfo := range incomingMembers {
		found = localInfo.ID == incomingInfo.DqliteID

		if found {
			localInfo.Address = incomingInfo.Address
			break
		}
	}

	if !found {
		return fmt.Errorf("Missing local cluster member in incoming recovery.yaml")
	}

	err = writeYaml(recoveryInfoYamlPath, localInfo)
	if err != nil {
		return err
	}

	// Update the local trust store with the incoming cluster configuration
	err = updateTrustStore(filesystem.TrustDir, incomingMembers)
	if err != nil {
		return err
	}

	err = CreateDatabaseBackup(filesystem)
	if err != nil {
		return err
	}

	// Now that we're as sure as we can be that the recovery DB is valid, we can
	// replace the existing DB
	err = os.RemoveAll(filesystem.DatabaseDir)
	if err != nil {
		return err
	}

	err = os.Remove(recoveryYamlPath)
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

	// Update daemon.yaml
	newAddress, err := types.ParseAddrPort(localInfo.Address)
	if err != nil {
		return fmt.Errorf("Failed to update daemon.yaml: %w", err)
	}

	daemonConfig := config.NewDaemonConfig(path.Join(filesystem.StateDir, "daemon.yaml"))
	err = daemonConfig.Load()
	if err != nil {
		return fmt.Errorf("Failed to load daemon.yaml: %w", err)
	}

	daemonConfig.SetAddress(newAddress)
	err = daemonConfig.Write()
	if err != nil {
		return fmt.Errorf("Failed to update daemon.yaml: %w", err)
	}

	return nil
}

// CreateDatabaseBackup writes a tarball of filesystem.DatabaseDir to
// filesystem.StateDir as db_backup.TIMESTAMP.tar.gz. It does not check to
// to ensure that the database is stopped.
func CreateDatabaseBackup(filesystem *sys.OS) error {
	// tar interprets `:` as a remote drive; ISO8601 allows a 'basic format'
	// with the colons omitted (as opposed to time.RFC3339)
	// https://en.wikipedia.org/wiki/ISO_8601
	backupFileName := fmt.Sprintf("db_backup.%s.tar.gz", time.Now().Format("2006-01-02T150405Z0700"))

	backupFilePath := path.Join(filesystem.StateDir, backupFileName)

	logger.Info("Creating database backup", logger.Ctx{"archive": backupFilePath})

	// For DB backups the tarball should contain the subdirs (usually `database/`)
	// so that the user can easily untar the backup from the state dir.
	rootDir := filesystem.StateDir
	walkDir, err := filepath.Rel(filesystem.StateDir, filesystem.DatabaseDir)

	// Don't bother if DatabaseDir is not inside StateDir
	if err != nil {
		logger.Warn("DB backup: DatabaseDir (%q) not in StateDir (%q)", logger.Ctx{
			"databaseDir": filesystem.DatabaseDir,
			"stateDir":    filesystem.StateDir,
		})
		rootDir = filesystem.DatabaseDir
		walkDir = "."
	}

	err = createTarball(backupFilePath, rootDir, walkDir, []string{})
	if err != nil {
		return fmt.Errorf("database backup: %w", err)
	}

	return nil
}

// createTarball creates tarball at tarballPath, rooted at rootDir and including
// all files in walkDir except those paths found in excludeFiles.
// walkDir and excludeFiles elements are relative to rootDir.
func createTarball(tarballPath string, rootDir string, walkDir string, excludeFiles []string) error {
	tarball, err := os.Create(tarballPath)
	if err != nil {
		return err
	}

	gzWriter := gzip.NewWriter(tarball)
	tarWriter := tar.NewWriter(gzWriter)

	filesys := os.DirFS(rootDir)

	err = fs.WalkDir(filesys, walkDir, func(filepath string, stat fs.DirEntry, err error) error {
		if err != nil {
			logger.Warn("Failed to read file while creating tarball; skipping", logger.Ctx{"file": filepath, "err": err})
			return nil
		}

		if slices.Contains(excludeFiles, filepath) {
			return nil
		}

		info, err := stat.Info()
		if err != nil {
			return err
		}

		header, err := tar.FileInfoHeader(info, filepath)
		if err != nil {
			return fmt.Errorf("create tar header for %q: %w", filepath, err)
		}

		// header.Name is the basename of `stat` by default
		header.Name = filepath

		err = tarWriter.WriteHeader(header)
		if err != nil {
			return err
		}

		// Only write contents for regular files
		if header.Typeflag == tar.TypeReg {
			file, err := os.Open(path.Join(rootDir, filepath))
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

		return nil
	})
	if err != nil {
		return err
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

		switch header.Typeflag {
		case tar.TypeReg:
			file, err := os.Create(filepath)
			if err != nil {
				return err
			}

			countWritten, err := io.Copy(file, tarReader)
			if countWritten != header.Size {
				return fmt.Errorf("Mismatched written (%d) and size (%d) for entry %q in %q", countWritten, header.Size, header.Name, tarballPath)
			} else if err != nil {
				return err
			}
		case tar.TypeDir:
			err = os.MkdirAll(filepath, fs.FileMode(header.Mode&int64(fs.ModePerm)))
			if err != nil {
				return err
			}
		}
	}

	return nil
}
