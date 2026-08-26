package profile

import (
	"errors"
	"fmt"
	"math"

	"github.com/pgsty/farrow/internal/config"
	"github.com/pgsty/farrow/internal/image"
	"github.com/pgsty/farrow/internal/network/subnet"
)

const (
	MinScale     = 1
	DefaultScale = 1
	MaxScale     = 64
)

// Overrides are the only profile-wide resource mutations supported by v1.
// Callers must pass a scale in the inclusive 1..64 range; the CLI default is
// DefaultScale.
type Overrides struct {
	Scale             int
	Image             string
	ForceUniformImage bool
	NetworkCIDR       string
}

// LoadWithOverrides loads one embedded profile and applies its catalog policy.
func LoadWithOverrides(name string, overrides Overrides) (config.File, Descriptor, error) {
	file, descriptor, err := Load(name)
	if err != nil {
		return config.File{}, Descriptor{}, err
	}
	file, err = ApplyOverrides(file, descriptor, overrides)
	if err != nil {
		return config.File{}, Descriptor{}, err
	}
	return file, descriptor, nil
}

// ApplyOverrides returns a deep copy. Resource/image overrides retain their
// existing narrow scope. An explicit network override moves the one global
// /24 and preserves each node's static last octet; roots and disks never move.
func ApplyOverrides(source config.File, descriptor Descriptor, overrides Overrides) (config.File, error) {
	result := cloneFile(source)
	if err := result.Validate(); err != nil {
		return config.File{}, fmt.Errorf("validate profile %s before overrides: %w", descriptor.Name, err)
	}
	if descriptor.Name == "" || descriptor.Name != result.Name {
		return config.File{}, fmt.Errorf("profile descriptor %q does not match configuration %q", descriptor.Name, result.Name)
	}
	if descriptor.ImagePolicy != ImageHomogeneous && descriptor.ImagePolicy != ImageMixed {
		return config.File{}, fmt.Errorf("profile %s has unsupported image policy %q", descriptor.Name, descriptor.ImagePolicy)
	}

	scale := overrides.Scale
	if scale < MinScale || scale > MaxScale {
		return config.File{}, fmt.Errorf("profile scale must be in range 1..64, got %d", overrides.Scale)
	}
	if !descriptor.Scalable && scale != 1 {
		return config.File{}, fmt.Errorf("profile %s is not scalable; scale must be 1", descriptor.Name)
	}
	if scale != 1 {
		var err error
		result.Defaults.CPUs, err = multiplyInt(result.Defaults.CPUs, scale)
		if err != nil {
			return config.File{}, fmt.Errorf("scale profile %s default CPUs: %w", descriptor.Name, err)
		}
		result.Defaults.Memory, err = multiplySize(result.Defaults.Memory, scale)
		if err != nil {
			return config.File{}, fmt.Errorf("scale profile %s default memory: %w", descriptor.Name, err)
		}
		for index := range result.Nodes {
			result.Nodes[index].CPUs, err = multiplyInt(result.Nodes[index].CPUs, scale)
			if err != nil {
				return config.File{}, fmt.Errorf("scale profile %s node %s CPUs: %w", descriptor.Name, result.Nodes[index].Name, err)
			}
			result.Nodes[index].Memory, err = multiplySize(result.Nodes[index].Memory, scale)
			if err != nil {
				return config.File{}, fmt.Errorf("scale profile %s node %s memory: %w", descriptor.Name, result.Nodes[index].Name, err)
			}
		}
	}

	if overrides.ForceUniformImage && overrides.Image == "" {
		return config.File{}, errors.New("force-uniform-image requires an image override")
	}
	if overrides.Image != "" {
		if descriptor.ImagePolicy == ImageMixed && !overrides.ForceUniformImage {
			return config.File{}, fmt.Errorf("profile %s mixes guest distributions; image override requires force-uniform-image", descriptor.Name)
		}
		uniformImage := image.CanonicalAlias(overrides.Image)
		result.Defaults.Image = uniformImage
		for index := range result.Nodes {
			result.Nodes[index].Image = uniformImage
		}
	}
	if overrides.NetworkCIDR != "" {
		sourceLayout, err := subnet.Parse(result.Network.CIDR)
		if err != nil {
			return config.File{}, fmt.Errorf("profile %s source network: %w", descriptor.Name, err)
		}
		targetLayout, err := subnet.Parse(overrides.NetworkCIDR)
		if err != nil {
			return config.File{}, err
		}
		result.Network.CIDR = targetLayout.CIDR()
		result.Network.HostAddress = targetLayout.HostAddress()
		result.Network.DHCPEnd = targetLayout.DHCPEnd()
		for index := range result.Nodes {
			address, err := targetLayout.RebaseStatic(result.Nodes[index].Address, sourceLayout)
			if err != nil {
				return config.File{}, fmt.Errorf("rebase profile %s node %s: %w", descriptor.Name, result.Nodes[index].Name, err)
			}
			result.Nodes[index].Address = address
		}
	}
	if err := result.Validate(); err != nil {
		return config.File{}, fmt.Errorf("profile %s overrides produce an invalid configuration: %w", descriptor.Name, err)
	}
	return result, nil
}

func multiplyInt(value, scale int) (int, error) {
	if value < 0 || scale < 1 || value > int(^uint(0)>>1)/scale {
		return 0, errors.New("integer overflow")
	}
	return value * scale, nil
}

func multiplySize(value config.Size, scale int) (config.Size, error) {
	if value < 0 || scale < 1 || int64(value) > math.MaxInt64/int64(scale) {
		return 0, errors.New("size overflow")
	}
	return value * config.Size(scale), nil
}

func cloneFile(source config.File) config.File {
	result := source
	result.Nodes = append([]config.NodeConfig(nil), source.Nodes...)
	for index := range source.Nodes {
		result.Nodes[index].HostAliases = append([]string(nil), source.Nodes[index].HostAliases...)
		result.Nodes[index].Disks = append([]config.DiskConfig(nil), source.Nodes[index].Disks...)
		result.Nodes[index].Forwards = append([]config.ForwardConfig(nil), source.Nodes[index].Forwards...)
	}
	return result
}
