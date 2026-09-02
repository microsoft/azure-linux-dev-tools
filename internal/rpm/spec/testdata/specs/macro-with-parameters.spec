Name:    macro-with-parameters
Version: 1.0
Release: 1
Summary: %%define macros that accept positional parameters
License: MIT

%define uname_suffix() %{?1:+%{1}}
%define uname_variant() %{lua:
    local v = rpm.expand("%{?1}")
    if v == "" then return "" end
    return "-" .. v
}

%define build_with(opt) \
%{expand:%%global _with_%{1} --with-%{1}} \
%global _enable_%{1} 1

%description
Fixture: parameterized %%define macros — empty-arg, lua body, and a
multi-line definition that itself expands further %%global calls.

%build
%{build_with foo}
%{build_with bar}
make %{?_smp_mflags}

%install
make install DESTDIR=%{buildroot}

%files
/usr/bin/macro-with-parameters

%changelog
* Thu Jan 01 1970 Builder <builder@example.com> - 1.0-1
- Initial fixture.
