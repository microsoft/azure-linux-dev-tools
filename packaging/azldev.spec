%global source_date_epoch_from_changelog 1

Name:           azldev
Version:        0.4.0
Release:        1%{?dist}
Summary:        Developer tooling for the Azure Linux distribution

License:        Apache-2.0 AND BSD-2-Clause AND BSD-3-Clause AND ISC AND MIT AND MPL-2.0
URL:            https://github.com/microsoft/azure-linux-dev-tools
Source0:        %{name}-%{version}.tar.gz

BuildRequires:  findutils
BuildRequires:  golang >= 1.26
BuildRequires:  go-rpm-macros

Recommends:     git-core
Suggests:       dnf-utils
Suggests:       kiwi-cli
Suggests:       mock
Suggests:       mock-rpmautospec

%description
azldev parses, resolves, and queries the TOML metadata that defines Azure
Linux. It prepares component sources for RPM builds, fetches source archives,
and provides utilities for locally building packages and images.

%prep
%autosetup -n %{name}-%{version}

%build
export GO111MODULE=on
export GOFLAGS="-mod=vendor -buildvcs=false"
build_date="$(date --utc --date="@${SOURCE_DATE_EPOCH}" +%Y-%m-%dT%H:%M:%SZ)"
export GO_LDFLAGS="-X go.szostok.io/version.version=%{version} -X go.szostok.io/version.buildDate=${build_date}"

%gobuild -o %{name} ./cmd/azldev

%install
install -Dpm0755 %{name} %{buildroot}%{_bindir}/%{name}

# azldev refuses to run as root unless AZLDEV_ALLOW_ROOT is set; rpmbuild runs %install as root.
export AZLDEV_ALLOW_ROOT=1

install -d %{buildroot}%{_datadir}/bash-completion/completions
%{buildroot}%{_bindir}/%{name} completion bash > %{buildroot}%{_datadir}/bash-completion/completions/%{name}

install -d %{buildroot}%{_datadir}/zsh/site-functions
%{buildroot}%{_bindir}/%{name} completion zsh > %{buildroot}%{_datadir}/zsh/site-functions/_%{name}

install -d %{buildroot}%{_datadir}/fish/vendor_completions.d
%{buildroot}%{_bindir}/%{name} completion fish > %{buildroot}%{_datadir}/fish/vendor_completions.d/%{name}.fish

find vendor -type f \( -iname 'LICENSE*' -o -iname 'COPYING*' -o -iname 'NOTICE*' \) -print0 |
while IFS= read -r -d '' license_file; do
    install -Dpm0644 "${license_file}" "%{buildroot}%{_licensedir}/%{name}/${license_file}"
done

%files
%license LICENSE
%license %{_licensedir}/%{name}/vendor
%doc README.md
%{_bindir}/%{name}
%{_datadir}/bash-completion/completions/%{name}
%{_datadir}/zsh/site-functions/_%{name}
%{_datadir}/fish/vendor_completions.d/%{name}.fish

%changelog
* Thu Sep 17 2026 Azure Linux Dev Tools <azldev@microsoft.com> - 0.4.0-1
- Add the initial RPM package
