Name:    multi-package-mixed
Version: 1.0
Release: 1
Summary: Multiple subpackages mixed with conditionals and macros
License: MIT
URL:     https://example.invalid/
Source0: %{name}-%{version}.tar.gz

%global commit_id 0123456789abcdef0123456789abcdef01234567
%define short_commit %(echo %{commit_id} | cut -c1-7)

%description
Fixture: realistic multi-subpackage layout combining %%package -n
renaming, mixed conditional wrappers, and shared macros. Exercises tag
walks, section enumeration, and per-package filtering against a
non-trivial topology.

%package devel
Summary: Development files for %{name}
Requires: %{name}%{?_isa} = %{version}-%{release}

%description devel
Headers and link-time helpers for building against %{name}.

%package -n lib%{name}
Summary: Runtime library for %{name}
Provides: bundled(%{name}-internal) = %{short_commit}

%description -n lib%{name}
Just the shared library, suitable for stand-alone consumption.

%if 0%{?with_docs}
%package doc
Summary: Documentation for %{name}
BuildArch: noarch

%description doc
HTML and man pages for %{name}, built from the in-tree sources.
%endif

%prep
%autosetup -n %{name}-%{version}

%build
%configure
%make_build

%install
%make_install

%files
%license LICENSE
/usr/bin/multi-package-mixed

%files devel
/usr/include/%{name}/

%files -n lib%{name}
/usr/lib64/lib%{name}.so.*

%if 0%{?with_docs}
%files doc
%doc README.md
%doc docs/html/
%endif

%changelog
* Thu Jan 01 1970 Builder <builder@example.com> - 1.0-1
- Initial fixture.
