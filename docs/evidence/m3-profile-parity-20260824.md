# M3 profile source and provider parity evidence

Date: 2026-08-24

## Scope and source lock

This evidence covers the 13 top-level Pigsty Vagrant profiles migrated into
Piglet. The source is locked to:

- repository: `https://github.com/pgsty/pigsty.git`
- local object store used for the check: `/Users/vonng/pgsty/pigsty`
- commit: `dab5dba333a070d96fde1f9feb41761148f2be8c`
- commit time: `2026-08-14T21:30:18+08:00`
- commit subject: `feat(cache): include Pigsty source in offline bundle`
- specs: `vagrant/spec/{all,citus,deb,deci,dual,full,meta,minio,oss,pro,rpm,simu,trio}.rb`
- provider templates: `vagrant/Vagrantfile.virtualbox` and
  `vagrant/Vagrantfile.libvirt`

Every source file was read with `git show <commit>:<path>`. The adjacent
Pigsty checkout had a pre-existing modification to `vagrant/Vagrantfile`, but
the check did not read that working-tree file and did not modify the adjacent
repository. Files under `vagrant/spec/example/` are outside the migrated set.

Both pinned provider templates define the same effective storage rules:

- root disk: `root_disk` from the spec, otherwise 64 GiB;
- ordinary node: `disk` from the spec, otherwise one 128 GiB disk at `/data`;
- any node whose name starts with `minio`: four 32 GiB disks at
  `/data1` through `/data4`.

The Vagrant `G`/`GB` sizes are represented as explicit `GiB` values in the
Piglet v1 migration contract. Source memory in MiB is likewise represented as
exact integral GiB values. The fixed Piglet envelope checked for every YAML is
`version: 1`, `arch: native`, private `10.10.10.0/24` networking with host
`.1` and DHCP end `.8`, `ssh.user: vagrant`, and the first node as the sole
control node.

The source box names are mapped to the formal Piglet image aliases:

| Pigsty box | Piglet alias |
| --- | --- |
| `cloud-image/rocky-9` | `el9` |
| `cloud-image/rocky-10` | `el10` |
| `cloud-image/debian-12` | `d12` |
| `cloud-image/debian-13` | `d13` |
| `cloud-image/ubuntu-22.04` | `u22` |
| `cloud-image/ubuntu-24.04` | `u24` |
| `cloud-image/ubuntu-26.04` | `u26` |

## Effective profile inventory

`Scalable: yes` means the profile catalog permits the bounded CPU/memory
scale factor. It does not change disks, IP addresses, or node count. `deci`
and `simu` retain the upstream non-scalable behavior. A mixed image policy
requires explicit acknowledgement before replacing every node image with one
uniform alias.

| Profile | Nodes | Effective images / policy | Scalable | Root disk | Effective data disks |
| --- | ---: | --- | :---: | --- | --- |
| `all` | 7 | `el9,el10,d12,d13,u22,u24,u26`; mixed | yes | 128 GiB each | one 128 GiB `/data` each |
| `citus` | 13 | `u24`; homogeneous | yes | 64 GiB each | one 128 GiB `/data` each; 1 meta plus 12 workers |
| `deb` | 5 | `d12,d13,u22,u24,u26`; mixed | yes | 128 GiB each | one 128 GiB `/data` each |
| `deci` | 10 | `u24`; homogeneous | no | 64 GiB each | one 128 GiB `/data` each |
| `dual` | 2 | `u24`; homogeneous | yes | 64 GiB each | one 128 GiB `/data` each |
| `full` | 4 | `u24`; homogeneous | yes | 64 GiB each | one 128 GiB `/data` each |
| `meta` | 1 | `u24`; homogeneous | yes | 64 GiB | one 128 GiB `/data` |
| `minio` | 4 | `u24`; homogeneous | yes | 64 GiB each | four 32 GiB disks at `/data1..4` each |
| `oss` | 7 | `el9,el10,d12,d13,u22,u24,u26`; mixed | yes | 128 GiB each | one 128 GiB `/data` each |
| `pro` | 7 | `el9,el10,d12,d13,u22,u24,u26`; mixed | yes | 128 GiB each | one 128 GiB `/data` each |
| `rpm` | 2 | `el9,el10`; mixed | yes | 128 GiB each | one 128 GiB `/data` each |
| `simu` | 20 | `u24`; homogeneous | no | 64 GiB each | `minio1..4`: four 32 GiB `/data1..4`; other 16: one 128 GiB `/data` |
| `trio` | 3 | `u24`; homogeneous | yes | 64 GiB each | one 128 GiB `/data` each |

Total: 13 profiles and 85 nodes.

## Strict YAML validation

The following command was run from `/Users/vonng/pgsty/piglet`:

```bash
set -e
for yaml in profiles/*.yaml; do
  ./bin/piglet validate --json -f "$PWD/$yaml" >/dev/null
  printf '%s: valid\n' "$(basename "$yaml")"
done
```

Result:

```text
all.yaml: valid
citus.yaml: valid
deb.yaml: valid
deci.yaml: valid
dual.yaml: valid
full.yaml: valid
meta.yaml: valid
minio.yaml: valid
oss.yaml: valid
pro.yaml: valid
rpm.yaml: valid
simu.yaml: valid
trio.yaml: valid
```

This proves each file passes Piglet's strict YAML decoder and v1 semantic
validation. It is separate from the upstream parity comparison below.

## Reproducible source plus provider parity check

The comparison evaluates only the 13 inspected, pinned Ruby literal specs. It
first asserts the effective root, ordinary-data, and `minio*` rules in both
pinned provider templates. For every source node it then compares node order
and count, name, IP, CPU, memory, effective image alias, explicit root size,
and every effective data disk name, size, and mount. It also checks the fixed
v1 envelope and the sole first-node control assignment.

This is the exact command that was run from
`/Users/vonng/pgsty/piglet`:

```bash
ruby -ryaml -ropen3 <<'RUBY'
repo = "/Users/vonng/pgsty/pigsty"
profile_dir = "/Users/vonng/pgsty/piglet/profiles"
commit = "dab5dba333a070d96fde1f9feb41761148f2be8c"
names = %w[all citus deb deci dual full meta minio oss pro rpm simu trio]
aliases = {
  "cloud-image/rocky-9" => "el9",
  "cloud-image/rocky-10" => "el10",
  "cloud-image/debian-12" => "d12",
  "cloud-image/debian-13" => "d13",
  "cloud-image/ubuntu-22.04" => "u22",
  "cloud-image/ubuntu-24.04" => "u24",
  "cloud-image/ubuntu-26.04" => "u26"
}

def git_show(repo, object)
  data, status = Open3.capture2("git", "-C", repo, "show", object)
  raise "git show failed: #{object}" unless status.success?
  data
end

providers = {
  "virtualbox" => git_show(repo, "#{commit}:vagrant/Vagrantfile.virtualbox"),
  "libvirt" => git_show(repo, "#{commit}:vagrant/Vagrantfile.libvirt")
}
providers.each do |name, text|
  [
    %q{root_disk_size = Integer(spec["root_disk"] || 64)},
    %q{disk_size = Integer(spec["disk"] || 128)},
    %q{spec["name"].start_with?("minio")}
  ].each { |needle| raise "#{name}: missing #{needle}" unless text.include?(needle) }
end
raise "virtualbox: minio disk contract mismatch" unless providers["virtualbox"].scan(/node\.vm\.disk :disk, name: "data[1-4]", size: "32GB"/).length == 4
raise "virtualbox: ordinary disk contract mismatch" unless providers["virtualbox"].include?(%q{node.vm.disk :disk, name: "data", size: "#{disk_size}GB"})
raise "libvirt: minio disk contract mismatch" unless providers["libvirt"].scan(/v\.storage :file, :size => '32G', :device => 'vd[b-e]'/).length == 4
raise "libvirt: ordinary disk contract mismatch" unless providers["libvirt"].include?(%q{v.storage :file, :size => "#{disk_size}G", :device => 'vdb'})
puts "provider virtualbox defaults/root/data/minio: parity ok"
puts "provider libvirt defaults/root/data/minio: parity ok"

total = 0
names.each do |profile|
  source = git_show(repo, "#{commit}:vagrant/spec/#{profile}.rb")
  Object.send(:remove_const, :Specs) if Object.const_defined?(:Specs)
  eval(source, TOPLEVEL_BINDING, "vagrant/spec/#{profile}.rb")
  expected = Specs
  yaml = YAML.safe_load(File.read(File.join(profile_dir, "#{profile}.yaml")))
  raise "#{profile}: fixed v1 envelope mismatch" unless yaml["version"] == 1 &&
    yaml["name"] == profile && yaml["arch"] == "native" &&
    yaml["network"] == {
      "mode" => "private", "cidr" => "10.10.10.0/24",
      "host_address" => "10.10.10.1", "dhcp_end" => "10.10.10.8"
    } && yaml["ssh"] == {"user" => "vagrant"}
  nodes = yaml.fetch("nodes")
  raise "#{profile}: node count mismatch" unless nodes.length == expected.length
  controls = nodes.each_index.select { |index| nodes[index]["control"] == true }
  raise "#{profile}: control node mismatch" unless controls == [0]

  expected.zip(nodes).each do |spec, node|
    id = "#{profile}/#{spec.fetch("name")}"
    actual_image = node["image"] || yaml.dig("defaults", "image")
    raise "#{id}: name mismatch" unless node["name"] == spec["name"]
    raise "#{id}: address mismatch" unless node["address"] == spec["ip"]
    raise "#{id}: image mismatch" unless actual_image == aliases.fetch(spec["image"])
    raise "#{id}: CPU mismatch" unless node["cpus"] == Integer(spec["cpu"])
    raise "#{id}: memory mismatch" unless node["memory"] == "#{Integer(spec["mem"]) / 1024}GiB"
    raise "#{id}: root disk mismatch" unless node["root_disk"] == "#{Integer(spec["root_disk"] || 64)}GiB"

    wanted = if spec["name"].start_with?("minio")
      (1..4).map { |i| {"name" => "data#{i}", "size" => "32GiB", "mount" => "/data#{i}"} }
    else
      [{"name" => "data", "size" => "#{Integer(spec["disk"] || 128)}GiB", "mount" => "/data"}]
    end
    actual = node.fetch("disks").map { |disk| disk.select { |key, _| %w[name size mount].include?(key) } }
    raise "#{id}: data disk mismatch" unless actual == wanted
    node.fetch("disks").each do |disk|
      raise "#{id}: filesystem mismatch" if disk.key?("filesystem") && disk["filesystem"] != "auto"
      raise "#{id}: persistence mismatch" if disk.key?("persistent") && disk["persistent"] != false
    end
  end
  total += nodes.length
  puts "#{profile}: source+provider parity ok (#{nodes.length} nodes)"
end
puts "TOTAL: 13 profiles, #{total} nodes, parity ok"
RUBY
```

Result:

```text
provider virtualbox defaults/root/data/minio: parity ok
provider libvirt defaults/root/data/minio: parity ok
all: source+provider parity ok (7 nodes)
citus: source+provider parity ok (13 nodes)
deb: source+provider parity ok (5 nodes)
deci: source+provider parity ok (10 nodes)
dual: source+provider parity ok (2 nodes)
full: source+provider parity ok (4 nodes)
meta: source+provider parity ok (1 nodes)
minio: source+provider parity ok (4 nodes)
oss: source+provider parity ok (7 nodes)
pro: source+provider parity ok (7 nodes)
rpm: source+provider parity ok (2 nodes)
simu: source+provider parity ok (20 nodes)
trio: source+provider parity ok (3 nodes)
TOTAL: 13 profiles, 85 nodes, parity ok
```

## YAML SHA-256 snapshot

Command:

```bash
shasum -a 256 profiles/*.yaml | LC_ALL=C sort -k2
```

Result:

```text
7a86109881ac51eec47836c38821e45755acee7507a13f2862fbee30f56d4e7a  profiles/all.yaml
ad9b44b1e9f0f2c2fb1143b11d7337ec15decd99e0985cf979702d249ee0eb71  profiles/citus.yaml
858e1a05193174c3be50d702fdee295c3f67897eb9c923c426913dd12bb5eb70  profiles/deb.yaml
4b9ace177116d99916b2e1e8665675bfaa7582335b0ad7c22233d603170dae96  profiles/deci.yaml
4853af64c097e24d668b4308ef6846a011134705a32170dc41523b01589ab3c9  profiles/dual.yaml
373ade3b60b88a7742a467d9870d734552abcd0a0ad9087aa0624b6d1efb308d  profiles/full.yaml
e663765abc0a2acdcebf166ace4eac33784f420eace24c04eb88bae18081e520  profiles/meta.yaml
6a7b5334911e3acd55fa3f76cfacd5e67c5ff94713c9f19c6b80bd8b61014bae  profiles/minio.yaml
d4afe3c4794e159cf6c5cf77a3bece141d120a6d579db6ac9a7f6b7aa4cb7c42  profiles/oss.yaml
74f1f27e965cce0d50ff109e9c745215fa2b7d0b2b5ac23abeda87b369db0f3d  profiles/pro.yaml
575d4e19135460d32272c5055b8bbbfacf2eb2149c5e970a68d85b48d8f8c643  profiles/rpm.yaml
34a644719c94d11be76d9569ab0e58d7cec42fb7c9bbc1c0884d9424beea98e5  profiles/simu.yaml
90a4fcf147b69a1a32769c69e20824c5975fff68a6525679c3f573a4dfc0f9ed  profiles/trio.yaml
```

These hashes cover only the reviewable YAML bytes and are intended to make
later profile drift obvious.

## Evidence boundary

This is static migration and validation evidence. It did not start QEMU, boot
any guest, exercise cloud-init, log in over SSH, create filesystems, or test
host-to-VM, VM-to-VM, or guest egress networking. It is therefore **not a
native VM E2E result** and must not be used as evidence for image bootability,
guest distribution behavior, runtime disk attachment, or private-network
connectivity.
