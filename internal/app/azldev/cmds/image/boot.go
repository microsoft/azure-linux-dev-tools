// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package image

import (
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev"
	"github.com/microsoft/azure-linux-dev-tools/internal/projectconfig"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/cloudinit"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileutils"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/iso"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/qemu"
	"github.com/samber/lo"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

const (
	tempDirPrefixBoot = "azlboot"

	// Default values for VM configuration.
	defaultSSHPort  = 8888
	defaultCPUs     = 8
	defaultDiskSize = "10G"

	// Default hostname for cloud-init metadata.
	defaultHostname = "azurelinux-vm"
)

// ImageFormat represents a bootable disk image format.
type ImageFormat string

const (
	// ImageFormatRaw is the raw disk image format.
	ImageFormatRaw ImageFormat = "raw"
	// ImageFormatQcow2 is the QEMU copy-on-write v2 format.
	ImageFormatQcow2 ImageFormat = "qcow2"
	// ImageFormatVhd is the Hyper-V VHD format (QEMU driver: vpc).
	ImageFormatVhd ImageFormat = "vhd"
	// ImageFormatVhdx is the Hyper-V virtual hard disk format.
	ImageFormatVhdx ImageFormat = "vhdx"
)

// SupportedImageFormats returns the list of supported bootable image formats in priority order.
// When multiple formats exist, the first match in this order is selected.
func SupportedImageFormats() []string {
	return []string{string(ImageFormatRaw), string(ImageFormatQcow2), string(ImageFormatVhdx), string(ImageFormatVhd)}
}

// Assert that [ImageFormat] implements the [pflag.Value] interface.
var _ pflag.Value = (*ImageFormat)(nil)

func (f *ImageFormat) String() string {
	return string(*f)
}

// Set parses and validates the image format value from a string.
func (f *ImageFormat) Set(value string) error {
	switch value {
	case string(ImageFormatRaw):
		*f = ImageFormatRaw
	case string(ImageFormatQcow2):
		*f = ImageFormatQcow2
	case string(ImageFormatVhd):
		*f = ImageFormatVhd
	case string(ImageFormatVhdx):
		*f = ImageFormatVhdx
	default:
		return fmt.Errorf("unsupported image format %#q; supported: %v", value, SupportedImageFormats())
	}

	return nil
}

// Type returns a descriptive string used in command-line help.
func (f *ImageFormat) Type() string {
	return "format"
}

// QEMUDriver returns the QEMU block driver name for the image format.
// Most formats use their string value directly, but some (e.g., vhd → vpc)
// require translation.
func QEMUDriver(format string) string {
	switch format {
	case string(ImageFormatVhd):
		return "vpc"
	default:
		return format
	}
}

// ImageBootOptions contains options for the boot command.
type ImageBootOptions struct {
	// VM configuration (applies to all boot modes).
	Arch       qemu.Arch
	SecureBoot bool
	SSHPort    uint16
	CPUs       int
	Memory     string

	// Image source: at least one must be provided (unless '--iso' is used, in which case
	// an empty disk is created if no image is specified).
	ImageName string      // Positional arg: look up image in project config to find its path.
	ImagePath string      // --image-path: explicit path to a disk image (overrides lookup).
	Format    ImageFormat // --format: explicit format hint for image-name lookup.

	// --iso: bootable ISO (e.g., livecd, installer, rescue media). Attached as a
	// bootable CD-ROM alongside the disk. Optional; independent of cloud-init.
	ISOPath  string
	DiskSize string // --disk-size: size of the empty qcow2 created when '--iso' is used without a disk.

	// Disk behavior.
	UseDiskRW bool // --rwdisk: persist writes to the source disk image.

	// Cloud-init configuration. If any credential is provided, a seed ISO is generated
	// and attached. Whether the booted system consumes it depends on cloud-init being
	// installed and enabled in the guest (same caveat applies to disk and ISO images).
	TestUserName            string
	TestUserPassword        string
	TestUserPasswordFile    string
	AuthorizedPublicKeyPath string
}

func bootOnAppInit(_ *azldev.App, parentCmd *cobra.Command) {
	parentCmd.AddCommand(NewImageBootCmd())
}

// bootCmdLongDescription is the Long help text for 'image boot'.
const bootCmdLongDescription = `Boot an Azure Linux image in a QEMU virtual machine.

This command starts a QEMU VM with the specified disk image and/or bootable ISO.
SSH is forwarded to the host on the specified port (default 8888). A cloud-init
NoCloud seed ISO is generated and attached whenever the user supplies any
user-provisioning intent — credentials ('--test-password'/'--test-password-file'
or '--authorized-public-key') or an explicit '--test-user'. The guest will
consume the seed only if cloud-init is installed and enabled.

Image sources (at least one is required):
  - IMAGE_NAME (positional):  Look up a built image in the project output directory.
  - '--image-path':           Explicit path to a disk image (may also be combined with
                              IMAGE_NAME to override the default location).
  - '--iso':                  Bootable ISO (livecd, installer, rescue). May be combined
                              with a disk image, or used alone to boot an empty disk.

When '--iso' is used without a disk image, an ephemeral empty qcow2 disk is
created (size set via '--disk-size') for the live/installer ISO to install onto.
The disk lives in a temp directory and is deleted when the VM exits; it is not
preserved between runs. The VM console is serial-only (-nographic), so the ISO
must support serial console interaction.

Requirements:
  - qemu-system-x86_64/qemu-system-aarch64 (QEMU emulator)
  - genisoimage (only when cloud-init credentials are provided)
  - qemu-img (only when creating an empty disk for '--iso')
  - sudo (for running QEMU with KVM)
  - OVMF firmware (for UEFI boot)`

// bootCmdExamples is the Example help text for 'image boot'.
const bootCmdExamples = `  # Boot an image by name
  azldev image boot my-image --test-password-file ~/.azl-test-pw

  # Boot from an explicit image path
  azldev image boot --image-path ./out/my-image.qcow2 --test-password secret

  # Boot with SSH on a custom port and extra memory
  azldev image boot my-image --test-password-file ~/.azl-test-pw --ssh-port 2222 --memory 8G

  # Boot from an ISO (livecd / installer) onto a new empty 20G disk
  azldev image boot --iso ~/Downloads/azurelinux.iso --disk-size 20G

  # Boot an existing disk image with a rescue ISO attached
  azldev image boot --image-path ./out/my-image.qcow2 --iso ~/Downloads/rescue.iso

  # Boot from a live ISO with cloud-init credentials (consumed if the live image
  # has cloud-init installed; otherwise harmlessly ignored)
  azldev image boot --iso ~/Downloads/livecd.iso --test-password secret`

// NewImageBootCmd constructs a [cobra.Command] for the 'image boot' command.
func NewImageBootCmd() *cobra.Command {
	options := &ImageBootOptions{}

	cmd := &cobra.Command{
		Use:               "boot [IMAGE_NAME]",
		Short:             "Boot an Azure Linux image in a QEMU VM",
		Long:              bootCmdLongDescription,
		Example:           bootCmdExamples,
		Args:              cobra.MaximumNArgs(1),
		ValidArgsFunction: generateImageNameCompletions,
	}

	cmd.RunE = azldev.RunFuncWithoutRequiredConfigWithExtraArgs(func(env *azldev.Env, args []string) (interface{}, error) {
		if len(args) > 0 {
			options.ImageName = args[0]
		}

		explicit := bootFlagsExplicit{
			DiskSize: cmd.Flags().Lookup("disk-size").Changed,
			TestUser: cmd.Flags().Lookup("test-user").Changed,
		}

		return nil, bootImage(env, options, explicit)
	})

	cmd.Flags().StringVarP(&options.ImagePath, "image-path", "i", "",
		"Path to the disk image file (overrides positional image name)")
	_ = cmd.MarkFlagFilename("image-path")

	cmd.Flags().StringVar(&options.ISOPath, "iso", "",
		"Path to an ISO file to boot from (livecd, installer, or rescue media)")
	_ = cmd.MarkFlagFilename("iso")

	cmd.Flags().StringVar(&options.DiskSize, "disk-size", defaultDiskSize,
		"Size of the empty virtual disk created for ISO boot (e.g., 10G, 20G, 512M)")

	addBootFlags(cmd, options)

	return cmd
}

func addBootFlags(cmd *cobra.Command, options *ImageBootOptions) {
	cmd.Flags().VarP(&options.Format, "format", "f",
		"Image format to boot (raw, qcow2, vhdx, vhd). Auto-detected if not specified.")

	cmd.Flags().StringVar(&options.TestUserName, "test-user", "test", "Name for the test account")
	cmd.Flags().StringVar(&options.TestUserPassword, "test-password", "",
		"Password for the test account (visible in process list; prefer --test-password-file)")
	cmd.Flags().StringVar(&options.TestUserPasswordFile, "test-password-file", "",
		"Path to file containing the password for the test account")
	_ = cmd.MarkFlagFilename("test-password-file")
	cmd.MarkFlagsMutuallyExclusive("test-password", "test-password-file")

	cmd.Flags().StringVar(&options.AuthorizedPublicKeyPath, "authorized-public-key", "",
		"Path to public key authorized for SSH to test user account")
	_ = cmd.MarkFlagFilename("authorized-public-key")

	cmd.Flags().BoolVar(&options.UseDiskRW, "rwdisk", false, "Allow writes to persist to the disk image")
	cmd.Flags().BoolVar(&options.SecureBoot, "secure-boot", false, "Enable secure boot for the VM")

	cmd.Flags().Uint16Var(&options.SSHPort, "ssh-port", defaultSSHPort, "Host port to forward to guest SSH (port 22)")
	cmd.Flags().IntVar(&options.CPUs, "cpus", defaultCPUs, "Number of CPU cores for the VM")
	cmd.Flags().StringVar(&options.Memory, "memory", "4G", "Amount of memory for the VM (e.g., 4G, 8192M)")
	cmd.Flags().Var(&options.Arch, "arch", "Target architecture (x86_64, aarch64). Defaults to host arch.")

	// Register shell completions for flags.
	_ = cmd.RegisterFlagCompletionFunc("format",
		func(_ *cobra.Command, _ []string, _ string) ([]string, cobra.ShellCompDirective) {
			return SupportedImageFormats(), cobra.ShellCompDirectiveNoFileComp
		})
	_ = cmd.RegisterFlagCompletionFunc("arch",
		func(_ *cobra.Command, _ []string, _ string) ([]string, cobra.ShellCompDirective) {
			return qemu.SupportedArchitectures(), cobra.ShellCompDirectiveNoFileComp
		})

	// '--format' only affects image-name lookup; it conflicts with an explicit path.
	cmd.MarkFlagsMutuallyExclusive("image-path", "format")
}

// bootFlagsExplicit tracks which flags were explicitly set on the command line
// (vs. left at their default). Used to distinguish "user said nothing" from
// "user said the default".
type bootFlagsExplicit struct {
	DiskSize bool
	TestUser bool
}

func bootImage(env *azldev.Env, options *ImageBootOptions, explicit bootFlagsExplicit) error {
	needEmptyDisk := options.ISOPath != "" && options.ImagePath == "" && options.ImageName == ""

	if err := validateBootOptions(options, explicit.DiskSize, needEmptyDisk); err != nil {
		return err
	}

	if err := resolveTestPassword(env, options); err != nil {
		return err
	}

	// Cloud-init is needed whenever the user asked for any user-provisioning behavior:
	// credentials (password / SSH key) OR an explicit '--test-user' (which signals
	// they want the named account created even if no auth is supplied — otherwise
	// '--test-user' would be silently ignored).
	needCloudInit := shouldBuildCloudInit(options, explicit.TestUser)

	hasLoginCredential := options.TestUserPassword != "" || options.AuthorizedPublicKeyPath != ""
	if !hasLoginCredential {
		slog.Warn("No test password ('--test-password'/'--test-password-file') or SSH key " +
			"('--authorized-public-key') supplied; you may have no way to log in to the booted OS")
	}

	if err := verifyISOExists(env, options.ISOPath); err != nil {
		return err
	}

	arch := string(options.Arch)
	if arch == "" {
		arch = qemu.GoArchToQEMUArch(runtime.GOARCH)
	}

	warnRWDisk(options, needEmptyDisk)

	if err := checkBootPrerequisites(env, arch, needEmptyDisk, needCloudInit); err != nil {
		return err
	}

	imagePath, imageFormat, err := resolveDiskSource(env, options)
	if err != nil {
		return err
	}

	if env.DryRun() {
		slog.Info("Dry-run: would boot VM",
			slog.String("iso", options.ISOPath),
			slog.String("disk", imagePath),
			slog.String("disk-format", imageFormat),
			slog.String("arch", arch),
			slog.Bool("empty-disk", needEmptyDisk),
			slog.Bool("cloud-init", needCloudInit),
		)

		return nil
	}

	return runQEMUBoot(env, options, arch, imagePath, imageFormat, needEmptyDisk, needCloudInit)
}

func validateBootOptions(options *ImageBootOptions, diskSizeExplicit, needEmptyDisk bool) error {
	if options.ISOPath == "" && options.ImagePath == "" && options.ImageName == "" {
		return errors.New("must specify at least one of: IMAGE_NAME, '--image-path', or '--iso'")
	}

	if diskSizeExplicit && !needEmptyDisk {
		return errors.New("'--disk-size' only applies when '--iso' is used without a disk image " +
			"(IMAGE_NAME or '--image-path')")
	}

	if needEmptyDisk && strings.TrimSpace(options.DiskSize) == "" {
		return errors.New("'--disk-size' must be non-empty when '--iso' is used without a disk image")
	}

	// QEMU uses ',' as a separator in '-drive' option strings. A comma in a file path
	// can silently corrupt the parse. Reject such paths up front with a clear error.
	if err := rejectCommaInPath("--image-path", options.ImagePath); err != nil {
		return err
	}

	if err := rejectCommaInPath("--iso", options.ISOPath); err != nil {
		return err
	}

	return nil
}

// rejectCommaInPath returns an error if the given path contains a comma. QEMU's
// '-drive' option syntax uses ',' as a key=value separator, so a comma in a file
// path can confuse the parser. We refuse such paths rather than silently escaping.
func rejectCommaInPath(flag, path string) error {
	if strings.Contains(path, ",") {
		return fmt.Errorf("path supplied via %#q contains a ',' character, which is not "+
			"supported because QEMU '-drive' argument syntax uses ',' as a separator; "+
			"please rename or move the file: %#q", flag, path)
	}

	return nil
}

// shouldBuildCloudInit reports whether a cloud-init NoCloud seed ISO should be
// generated and attached. It returns true when the user supplied any
// user-provisioning intent: credentials (password or SSH key) OR an explicit
// '--test-user' (otherwise '--test-user' would be silently ignored).
func shouldBuildCloudInit(options *ImageBootOptions, testUserExplicit bool) bool {
	return options.TestUserPassword != "" ||
		options.AuthorizedPublicKeyPath != "" ||
		testUserExplicit
}

func resolveTestPassword(env *azldev.Env, options *ImageBootOptions) error {
	if options.TestUserPasswordFile == "" {
		return nil
	}

	passwordBytes, err := fileutils.ReadFile(env.FS(), options.TestUserPasswordFile)
	if err != nil {
		return fmt.Errorf("failed to read password file %#q:\n%w", options.TestUserPasswordFile, err)
	}

	options.TestUserPassword = strings.TrimSpace(string(passwordBytes))

	return nil
}

func verifyISOExists(env *azldev.Env, isoPath string) error {
	if isoPath == "" {
		return nil
	}

	exists, err := fileutils.Exists(env.FS(), isoPath)
	if err != nil {
		return fmt.Errorf("failed to check ISO file %#q:\n%w", isoPath, err)
	}

	if !exists {
		return fmt.Errorf("ISO file %#q not found", isoPath)
	}

	return nil
}

func warnRWDisk(options *ImageBootOptions, needEmptyDisk bool) {
	if !options.UseDiskRW {
		return
	}

	if needEmptyDisk {
		slog.Warn("'--rwdisk' has no effect with '--iso' alone; the empty disk is ephemeral")
	} else {
		slog.Warn("'--rwdisk' enabled: changes will persist to the source disk image")
	}
}

// resolveDiskSource returns the source disk image path and format (empty strings if
// no source disk was requested — i.e., '--iso' is used alone). If IMAGE_NAME is
// supplied, its presence in project config is validated regardless of whether
// '--image-path' overrides the file location.
func resolveDiskSource(env *azldev.Env, options *ImageBootOptions) (imagePath, imageFormat string, err error) {
	if options.ImageName != "" {
		if _, err := ResolveImageByName(env, options.ImageName); err != nil {
			return "", "", err
		}
	}

	switch {
	case options.ImagePath != "":
		imageFormat, err = InferImageFormat(options.ImagePath)
		if err != nil {
			return "", "", err
		}

		imagePath = options.ImagePath

		slog.Info("Using disk image",
			slog.String("path", imagePath),
			slog.String("format", imageFormat),
		)
	case options.ImageName != "":
		imagePath, imageFormat, err = findBootableImageArtifact(env, options.ImageName, string(options.Format))
		if err != nil {
			return "", "", err
		}

		slog.Info("Resolved image artifact",
			slog.String("image", options.ImageName),
			slog.String("path", imagePath),
			slog.String("format", imageFormat),
		)
	}

	return imagePath, imageFormat, nil
}

// runQEMUBoot performs the actual QEMU boot: prepares the temp dir, creates any
// transient artifacts (empty disk, ephemeral disk copy, cloud-init seed ISO), and
// invokes QEMU. All boot modes converge here.
func runQEMUBoot(
	env *azldev.Env,
	options *ImageBootOptions,
	arch, imagePath, imageFormat string,
	needEmptyDisk, needCloudInit bool,
) (err error) {
	bootEnv, err := prepareQEMUBootEnv(env, arch, options.SecureBoot)
	if err != nil {
		return err
	}

	defer fileutils.RemoveAllAndUpdateErrorIfNil(env.FS(), bootEnv.tempDir, &err)

	// Prepare the disk: either create an empty qcow2, or use/copy the source disk.
	var diskPath, diskFormat string

	switch {
	case needEmptyDisk:
		diskPath = filepath.Join(bootEnv.tempDir, "disk.qcow2")
		diskFormat = string(ImageFormatQcow2)

		slog.Info("Creating empty qcow2 disk",
			slog.String("path", diskPath),
			slog.String("size", options.DiskSize),
		)

		err = bootEnv.runner.CreateEmptyQcow2(env, diskPath, options.DiskSize)
		if err != nil {
			return fmt.Errorf("creating empty disk:\n%w", err)
		}
	default:
		diskFormat = imageFormat

		diskPath, err = prepareDiskForBoot(env, options, bootEnv.tempDir, imagePath, imageFormat)
		if err != nil {
			return err
		}
	}

	// Build the cloud-init seed ISO if any cloud-init credentials were provided.
	cloudInitISOPath := ""

	if needCloudInit {
		cloudInitISOPath = filepath.Join(bootEnv.tempDir, "cloud-init.iso")

		err = buildCloudInitMetadataIso(env, options, cloudInitISOPath)
		if err != nil {
			return err
		}
	}

	slog.Info("Booting VM",
		slog.String("iso", options.ISOPath),
		slog.String("disk", diskPath),
		slog.String("disk-format", diskFormat),
		slog.String("arch", arch),
	)

	err = bootEnv.runner.Run(env, qemu.RunOptions{
		Arch:             arch,
		FirmwarePath:     bootEnv.firmwarePath,
		NVRAMPath:        bootEnv.nvramPath,
		DiskPath:         diskPath,
		DiskType:         QEMUDriver(diskFormat),
		CloudInitISOPath: cloudInitISOPath,
		InstallISOPath:   options.ISOPath,
		SecureBoot:       options.SecureBoot,
		SSHPort:          int(options.SSHPort),
		CPUs:             options.CPUs,
		Memory:           options.Memory,
	})
	if err != nil {
		return fmt.Errorf("running QEMU:\n%w", err)
	}

	return nil
}

// createBootTempDir creates a temporary directory for boot artifacts. It uses the project
// work directory if available, otherwise falls back to the OS temp directory (resolved
// via the injected filesystem).
func createBootTempDir(env *azldev.Env) (string, error) {
	if env.WorkDir() != "" {
		tempDir, err := fileutils.MkdirTemp(env.FS(), env.WorkDir(), tempDirPrefixBoot)
		if err != nil {
			return "", fmt.Errorf("failed to create boot temp dir:\n%w", err)
		}

		return tempDir, nil
	}

	tempDir, err := fileutils.MkdirTempInTempDir(env.FS(), tempDirPrefixBoot)
	if err != nil {
		return "", fmt.Errorf("failed to create boot temp dir:\n%w", err)
	}

	return tempDir, nil
}

// fileExtensionsForFormat returns the file extensions to search for the given format.
// Most formats have a single extension matching the format name, but vhd accepts
// both .vhd and .vhdfixed since QEMU treats them identically.
func fileExtensionsForFormat(format string) []string {
	if format == string(ImageFormatVhd) {
		return []string{"vhd", "vhdfixed"}
	}

	return []string{format}
}

// findBootableImageArtifact locates a bootable image artifact in the output directory for the
// given image name. If format is specified, only that format is searched. Otherwise, formats
// are searched in priority order (raw, qcow2, vhdx, vhd) and the first match is returned.
func findBootableImageArtifact(
	env *azldev.Env, imageName, format string,
) (imagePath, imageFormat string, err error) {
	// First validate the image exists in project configuration.
	_, err = ResolveImageByName(env, imageName)
	if err != nil {
		return "", "", err
	}

	// Construct the output directory path where built images are stored.
	imageOutputDir := filepath.Join(env.OutputDir(), "images", imageName)

	exists, err := fileutils.Exists(env.FS(), imageOutputDir)
	if err != nil {
		return "", "", fmt.Errorf("failed to check image output directory:\n%w", err)
	}

	if !exists {
		return "", "", fmt.Errorf(
			"image output directory %#q does not exist; run 'azldev image build %s' first",
			imageOutputDir, imageName,
		)
	}

	// Determine which formats to search.
	formatsToSearch := SupportedImageFormats()
	if format != "" {
		formatsToSearch = []string{format}
	}

	// Search for bootable artifacts in priority order.
	for _, currentFormat := range formatsToSearch {
		for _, ext := range fileExtensionsForFormat(currentFormat) {
			pattern := filepath.Join(imageOutputDir, "*."+ext)

			matches, globErr := fileutils.Glob(env.FS(), pattern)
			if globErr != nil {
				continue
			}

			if len(matches) > 0 {
				// Return the first match for the highest-priority format.
				return matches[0], currentFormat, nil
			}
		}
	}

	// No bootable artifact found - provide helpful error message.
	if format != "" {
		// Specific format requested but not found; list what is available.
		allArtifacts, _ := listImageArtifacts(env, imageOutputDir)
		if len(allArtifacts) > 0 {
			return "", "", fmt.Errorf(
				"no %#q format image found in %#q; available artifacts: %v",
				format, imageOutputDir, allArtifacts,
			)
		}

		return "", "", fmt.Errorf(
			"no %#q format image found in %#q; directory contains no image artifacts",
			format, imageOutputDir,
		)
	}

	return "", "", fmt.Errorf(
		"no bootable image artifact found in %#q; supported formats: %v",
		imageOutputDir, SupportedImageFormats(),
	)
}

// InferImageFormat determines the image format from the file extension.
// Returns an error if the extension does not match a supported format.
func InferImageFormat(imagePath string) (string, error) {
	ext := strings.ToLower(filepath.Ext(imagePath))
	if ext == "" {
		return "", fmt.Errorf(
			"cannot infer image format from %#q: no file extension",
			imagePath,
		)
	}

	// Strip the leading dot (e.g., ".vhdfixed" → "vhdfixed").
	format := ext[1:]
	if format == "vhdfixed" {
		format = string(ImageFormatVhd)
	}

	// Validate the inferred format is supported.
	supported := SupportedImageFormats()
	if !lo.Contains(supported, format) {
		return "", fmt.Errorf(
			"unsupported image format %#q inferred from %#q; supported formats: %v",
			format, imagePath, supported,
		)
	}

	return format, nil
}

// listImageArtifacts returns a list of image artifact filenames in the given directory.
func listImageArtifacts(env *azldev.Env, dir string) ([]string, error) {
	entries, err := fileutils.ReadDir(env.FS(), dir)
	if err != nil {
		return nil, fmt.Errorf("failed to read directory %#q:\n%w", dir, err)
	}

	var artifacts []string

	for _, entry := range entries {
		if !entry.IsDir() {
			artifacts = append(artifacts, entry.Name())
		}
	}

	return artifacts, nil
}

// ResolveImageByName is like resolveImage but includes a list of available
// images in the error message when the image is not found.
func ResolveImageByName(env *azldev.Env, imageName string) (*projectconfig.ImageConfig, error) {
	cfg := env.Config()
	if cfg == nil {
		return nil, errors.New("no project configuration loaded")
	}

	var err error

	// If an image name was provided and exists in the configuration, then return the config.
	if imageName != "" {
		if imageConfig, ok := cfg.Images[imageName]; ok {
			return &imageConfig, nil
		}

		err = fmt.Errorf("image %#q not found in project configuration", imageName)
	} else {
		err = errors.New("image name is required")
	}

	// Something went wrong; list available images so we can offer options in the error message.
	availableImages := lo.Keys(cfg.Images)
	sort.Strings(availableImages)

	if len(availableImages) == 0 {
		return nil, fmt.Errorf("%w; no images defined in project configuration", err)
	}

	return nil, fmt.Errorf(
		"%w; available images: %s",
		err, strings.Join(availableImages, ", "),
	)
}

func checkBootPrerequisites(env *azldev.Env, arch string, needQEMUImg, needGenisoimage bool) error {
	if err := qemu.CheckPrerequisites(env, arch); err != nil {
		return fmt.Errorf("checking QEMU prerequisites:\n%w", err)
	}

	if needQEMUImg {
		if err := qemu.CheckQEMUImgPrerequisite(env); err != nil {
			return fmt.Errorf("checking qemu-img prerequisites:\n%w", err)
		}
	}

	if needGenisoimage {
		if err := iso.CheckPrerequisites(env); err != nil {
			return fmt.Errorf("checking genisoimage prerequisites:\n%w", err)
		}
	}

	return nil
}

// qemuBootEnv holds the common QEMU boot environment shared by all boot modes.
type qemuBootEnv struct {
	runner       *qemu.Runner
	tempDir      string
	firmwarePath string
	nvramPath    string
}

// prepareQEMUBootEnv sets up the common QEMU boot environment: runner, temp dir,
// firmware, and NVRAM. The caller must clean up tempDir (e.g., via defer).
func prepareQEMUBootEnv(env *azldev.Env, arch string, secureBoot bool) (*qemuBootEnv, error) {
	runner := qemu.NewRunner(env)

	fwPath, nvramTemplatePath, err := runner.FindFirmware(arch, secureBoot)
	if err != nil {
		return nil, fmt.Errorf("finding VM firmware:\n%w", err)
	}

	tempDir, err := createBootTempDir(env)
	if err != nil {
		return nil, fmt.Errorf("failed to create temp dir:\n%w", err)
	}

	nvramPath := filepath.Join(tempDir, "nvram.bin")

	err = fileutils.CopyFile(env, env.FS(), nvramTemplatePath, nvramPath, fileutils.CopyFileOptions{})
	if err != nil {
		// Clean up temp dir since the caller won't have a chance to.
		fileutils.RemoveAllAndUpdateErrorIfNil(env.FS(), tempDir, &err)

		return nil, fmt.Errorf("failed to copy NVRAM template file:\n%w", err)
	}

	return &qemuBootEnv{
		runner:       runner,
		tempDir:      tempDir,
		firmwarePath: fwPath,
		nvramPath:    nvramPath,
	}, nil
}

// prepareDiskForBoot prepares the disk image for booting. If UseDiskRW is false,
// it creates an ephemeral copy to avoid modifying the original image.
func prepareDiskForBoot(
	env *azldev.Env, options *ImageBootOptions, tempDir, imagePath, imageFormat string,
) (string, error) {
	if options.UseDiskRW {
		return imagePath, nil
	}

	ephemeralPath := filepath.Join(tempDir, "ephemeral."+imageFormat)

	err := fileutils.CopyFile(env, env.FS(), imagePath, ephemeralPath, fileutils.CopyFileOptions{})
	if err != nil {
		return "", fmt.Errorf("failed to copy disk image:\n%w", err)
	}

	return ephemeralPath, nil
}

func buildCloudInitMetadataIso(env *azldev.Env, options *ImageBootOptions, outputFilePath string) (err error) {
	tempDir, err := createBootTempDir(env)
	if err != nil {
		return fmt.Errorf("failed to create temp dir:\n%w", err)
	}

	defer fileutils.RemoveAllAndUpdateErrorIfNil(env.FS(), tempDir, &err)

	// Build cloud-init configuration for test user.
	cloudConfig, err := buildCloudInitConfig(env, options)
	if err != nil {
		return err
	}

	// Write cloud-init files.
	metaDataPath, userDataPath, err := cloudinit.WriteDataFiles(
		env, tempDir, defaultHostname, cloudConfig,
	)
	if err != nil {
		return fmt.Errorf("writing cloud-init files:\n%w", err)
	}

	// Create ISO using the iso package.
	isoRunner := iso.NewRunner(env)

	err = isoRunner.CreateISO(env, iso.CreateISOOptions{
		OutputPath:   outputFilePath,
		VolumeID:     "cidata",
		InputFiles:   []string{metaDataPath, userDataPath},
		UseJoliet:    true,
		UseRockRidge: true,
		Description:  "Creating cloud-init metadata ISO",
	})
	if err != nil {
		return fmt.Errorf("failed to generate cloud-init ISO:\n%w", err)
	}

	return nil
}

// buildCloudInitConfig creates the cloud-init configuration for the test user.
func buildCloudInitConfig(env *azldev.Env, options *ImageBootOptions) (*cloudinit.Config, error) {
	hasPassword := options.TestUserPassword != ""

	testUserConfig := cloudinit.UserConfig{
		Name:        options.TestUserName,
		Description: "Test User",
		Shell:       "/bin/bash",
		Sudo:        []string{"ALL=(ALL) NOPASSWD:ALL"},
		// Only unlock the password when one is actually provided. Otherwise the
		// account would be unlocked with no defined password, which combined with
		// SSH password auth could allow passwordless login.
		LockPassword:      lo.ToPtr(!hasPassword),
		PlainTextPassword: options.TestUserPassword,
		Groups:            []string{"sudo"},
	}

	if options.AuthorizedPublicKeyPath != "" {
		publicKeyBytes, err := fileutils.ReadFile(env.FS(), options.AuthorizedPublicKeyPath)
		if err != nil {
			return nil, fmt.Errorf("failed to read public key file %#q:\n%w", options.AuthorizedPublicKeyPath, err)
		}

		testUserConfig.SSHAuthorizedKeys = append(testUserConfig.SSHAuthorizedKeys, strings.TrimSpace(string(publicKeyBytes)))
	}

	return &cloudinit.Config{
		// Only enable SSH password auth when a password is actually provided.
		EnableSSHPasswordAuth: lo.ToPtr(hasPassword),
		DisableRootUser:       lo.ToPtr(true),
		ChangePasswords: &cloudinit.PasswordConfig{
			Expire: lo.ToPtr(false),
		},
		Users: []cloudinit.UserConfig{
			{
				Name: "default",
			},
			testUserConfig,
		},
	}, nil
}
