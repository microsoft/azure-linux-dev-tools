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
	defaultSSHPort = 8888
	defaultCPUs    = 8

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
	Arch                    qemu.Arch
	AuthorizedPublicKeyPath string
	UseDiskRW               bool
	ImageName               string
	ImagePath               string
	Format                  ImageFormat
	SecureBoot              bool
	TestUserName            string
	TestUserPassword        string
	TestUserPasswordFile    string
	SSHPort                 uint16
	CPUs                    int
	Memory                  string
}

func bootOnAppInit(_ *azldev.App, parentCmd *cobra.Command) {
	parentCmd.AddCommand(NewImageBootCmd())
}

// NewImageBootCmd constructs a [cobra.Command] for the 'image boot' command.
func NewImageBootCmd() *cobra.Command {
	options := &ImageBootOptions{}

	cmd := &cobra.Command{
		Use:   "boot [IMAGE_NAME]",
		Short: "Boot an Azure Linux image in a QEMU VM",
		Long: `Boot an Azure Linux image in a QEMU virtual machine.

This command starts a QEMU VM with the specified disk image, setting up a test user
via cloud-init for access. SSH is forwarded to the host on the specified port (default 8888).

The image can be specified either by name (positional argument) which will look up the
built image in the output directory, or by explicit path using --image-path.

Requirements:
  - qemu-system-x86_64/qemu-system-aarch64 (QEMU emulator)
  - genisoimage (for creating cloud-init ISO)
  - sudo (for running QEMU with KVM)
  - OVMF firmware (for UEFI boot)`,
		Example: `  # Boot an image by name
  azldev image boot my-image --test-password-file ~/.azl-test-pw

  # Boot from an explicit image path
  azldev image boot --image-path ./out/my-image.qcow2 --test-password secret

  # Boot with SSH on a custom port and extra memory
  azldev image boot my-image --test-password-file ~/.azl-test-pw --ssh-port 2222 --memory 8G`,
		Args: cobra.MaximumNArgs(1),
		RunE: azldev.RunFuncWithExtraArgs(func(env *azldev.Env, args []string) (interface{}, error) {
			if env.WorkDir() == "" {
				return nil, errors.New("work dir must be specified in config")
			}

			if len(args) > 0 {
				options.ImageName = args[0]
			}

			return nil, bootImage(env, options)
		}),
		ValidArgsFunction: generateImageNameCompletions,
	}

	cmd.Flags().StringVarP(&options.ImagePath, "image-path", "i", "",
		"Path to the disk image file (overrides positional image name)")
	_ = cmd.MarkFlagFilename("image-path")

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
	cmd.MarkFlagsOneRequired("test-password", "test-password-file")

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

	cmd.MarkFlagsMutuallyExclusive("image-path", "format")
}

func bootImage(env *azldev.Env, options *ImageBootOptions) error {
	// Resolve password from file if specified (do this before validation since
	// file reading is not validation).
	if options.TestUserPasswordFile != "" {
		passwordBytes, err := fileutils.ReadFile(env.FS(), options.TestUserPasswordFile)
		if err != nil {
			return fmt.Errorf("failed to read password file %#q:\n%w", options.TestUserPasswordFile, err)
		}

		options.TestUserPassword = strings.TrimSpace(string(passwordBytes))
	}

	// Default to host architecture if not specified.
	arch := string(options.Arch)
	if arch == "" {
		arch = qemu.GoArchToQEMUArch(runtime.GOARCH)
	}

	// Warn about persistent disk writes.
	if options.UseDiskRW {
		slog.Warn("--rwdisk enabled: changes will persist to the source disk image")
	}

	if err := checkBootPrerequisites(env, arch); err != nil {
		return err
	}

	// Resolve image path: explicit --image-path takes precedence, otherwise resolve from image name.
	imagePath := options.ImagePath
	imageFormat := string(options.Format)

	if imagePath == "" {
		if options.ImageName == "" {
			return errors.New("either IMAGE_NAME argument or --image-path must be specified")
		}

		var err error

		imagePath, imageFormat, err = findBootableImageArtifact(env, options.ImageName, imageFormat)
		if err != nil {
			return err
		}

		slog.Info("Resolved image artifact",
			slog.String("image", options.ImageName),
			slog.String("path", imagePath),
			slog.String("format", imageFormat),
		)
	} else {
		// Infer format from the file extension when --image-path is used.
		var err error

		imageFormat, err = InferImageFormat(imagePath)
		if err != nil {
			return err
		}

		slog.Info("Inferred image format from file extension",
			slog.String("path", imagePath),
			slog.String("format", imageFormat),
		)
	}

	// Dry-run mode: log what would be executed and return early.
	if env.DryRun() {
		slog.Info("Dry-run: would boot image",
			slog.String("path", imagePath),
			slog.String("format", imageFormat),
			slog.String("arch", arch),
		)

		return nil
	}

	return bootImageUsingDiskFile(env, options, arch, imagePath, imageFormat)
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

	if imageName == "" {
		return nil, errors.New("image name is required")
	}

	imageConfig, ok := cfg.Images[imageName]
	if !ok {
		// List available images in the error message.
		availableImages := lo.Keys(cfg.Images)
		sort.Strings(availableImages)

		if len(availableImages) == 0 {
			return nil, fmt.Errorf("image %#q not found; no images defined in project configuration", imageName)
		}

		return nil, fmt.Errorf(
			"image %#q not found in project configuration; available images: %s",
			imageName, strings.Join(availableImages, ", "),
		)
	}

	return &imageConfig, nil
}

func checkBootPrerequisites(env *azldev.Env, arch string) error {
	if err := qemu.CheckPrerequisites(env, arch); err != nil {
		return fmt.Errorf("checking QEMU prerequisites:\n%w", err)
	}

	if err := iso.CheckPrerequisites(env); err != nil {
		return fmt.Errorf("checking genisoimage prerequisites:\n%w", err)
	}

	return nil
}

func bootImageUsingDiskFile(
	env *azldev.Env, options *ImageBootOptions, arch, imagePath, imageFormat string,
) (err error) {
	qemuRunner := qemu.NewRunner(env)

	fwPath, nvramTemplatePath, err := qemuRunner.FindFirmware(arch, options.SecureBoot)
	if err != nil {
		return fmt.Errorf("finding VM firmware:\n%w", err)
	}

	tempDir, err := fileutils.MkdirTemp(env.FS(), env.WorkDir(), tempDirPrefixBoot)
	if err != nil {
		return fmt.Errorf("failed to create temp dir:\n%w", err)
	}

	defer fileutils.RemoveAllAndUpdateErrorIfNil(env.FS(), tempDir, &err)

	nvramPath := filepath.Join(tempDir, "nvram.bin")

	err = fileutils.CopyFile(env, env.FS(), nvramTemplatePath, nvramPath, fileutils.CopyFileOptions{})
	if err != nil {
		return fmt.Errorf("failed to copy NVRAM template file:\n%w", err)
	}

	cloudInitMetadataIsoPath := filepath.Join(tempDir, "cloud-init.iso")

	err = buildCloudInitMetadataIso(env, options, cloudInitMetadataIsoPath)
	if err != nil {
		return err
	}

	selectedDiskPath, err := prepareDiskForBoot(env, options, tempDir, imagePath, imageFormat)
	if err != nil {
		return err
	}

	err = qemuRunner.Run(env, qemu.RunOptions{
		Arch:             arch,
		FirmwarePath:     fwPath,
		NVRAMPath:        nvramPath,
		DiskPath:         selectedDiskPath,
		DiskType:         QEMUDriver(imageFormat),
		CloudInitISOPath: cloudInitMetadataIsoPath,
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
	tempDir, err := fileutils.MkdirTemp(env.FS(), env.WorkDir(), tempDirPrefixBoot)
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
	testUserConfig := cloudinit.UserConfig{
		Name:                  options.TestUserName,
		Description:           "Test User",
		EnableSSHPasswordAuth: lo.ToPtr(true),
		Shell:                 "/bin/bash",
		Sudo:                  []string{"ALL=(ALL) NOPASSWD:ALL"},
		LockPassword:          lo.ToPtr(false),
		PlainTextPassword:     options.TestUserPassword,
		Groups:                []string{"sudo"},
	}

	if options.AuthorizedPublicKeyPath != "" {
		publicKeyBytes, err := fileutils.ReadFile(env.FS(), options.AuthorizedPublicKeyPath)
		if err != nil {
			return nil, fmt.Errorf("failed to read public key file %#q:\n%w", options.AuthorizedPublicKeyPath, err)
		}

		testUserConfig.SSHAuthorizedKeys = append(testUserConfig.SSHAuthorizedKeys, strings.TrimSpace(string(publicKeyBytes)))
	}

	return &cloudinit.Config{
		EnableSSHPasswordAuth: lo.ToPtr(true),
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
