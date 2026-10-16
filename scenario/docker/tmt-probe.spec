Name: azldev-tmt-probe
Version: 1.0
Release: 1
Summary: Probe package for azldev virtual TMT end-to-end tests
License: MIT
BuildArch: noarch
Source0: tmt-probe.sh

%description
Installs a deterministic command used to verify candidate RPM installation and
execution in the azldev virtual TMT end-to-end test.

%install
install -D -m 0755 %{SOURCE0} %{buildroot}%{_bindir}/azldev-tmt-probe

%files
%{_bindir}/azldev-tmt-probe
