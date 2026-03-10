Name: a
BuildArch: noarch
Version: 1.2.3
Release: 4%{?dist}
Summary: A test component
License: MIT

%description
Test component for, you know, testing.

%build
echo hello >file.txt

%install
mkdir -p %{buildroot}/%{_datadir}/test-component
cp file.txt %{buildroot}/%{_datadir}/test-component/file.txt

%files
%{_datadir}/test-component

