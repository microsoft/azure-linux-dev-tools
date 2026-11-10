Name:    macro-conditional
Version: 1.0
Release: 1%{?dist}
Summary: Fixture with %if/%endif inside macro continuation bodies
License: MIT

# Parameterized macro with %if/%endif in the body (kernel pattern).
# The %if here is RPM macro body text, NOT a structural conditional.
%define kernel_reqprovconf(o) \
%if %{-o:0}%{!-o:1}\
Provides: kernel = %{version}-%{release}\
Provides: %{name} = %{version}-%{release}\
%endif\
%{nil}

# Global macro with conditional (ghc pattern).
%global obsoletes_pkg() \
%if %{defined old_name}\
Obsoletes: %{old_name}%{?1:-%1} < %{version}-%{release}\
Provides: %{old_name}%{?1:-%1} = %{version}-%{release}\
%endif\
%{nil}

# Real structural conditional (should still be parsed).
%if 0%{?fedora}
BuildRequires: fedora-only-dep
%endif

%description
A spec testing that %if/%endif inside backslash-continued macro
definitions are treated as macro body text, not structural conditionals.

%build
make %{?_smp_mflags}

%install
make install DESTDIR=%{buildroot}

%files
/usr/bin/macro-conditional

%changelog
* Thu Jan 01 1970 Builder <builder@example.com> - 1.0-1
- Initial build.
