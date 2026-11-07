Name:           sharenet
Version:        0.1.0
Release:        1%{?dist}
Summary:        Share your phone's VPN with this computer

License:        MIT
URL:            https://github.com/MogaleAlpheus/ShareNet
Source0:        %{name}-%{version}.tar.gz

BuildArch:      x86_64
BuildRequires:  systemd-rpm-macros
Requires:       iproute, systemd-resolved, polkit, NetworkManager
Requires:       libnotify
Recommends:     unzip

%description
ShareNet Linux client — turns your phone's VPN into a transparent system
tunnel. Works over USB (adb) or Wi-Fi, with a local web UI, live traffic
stats, and automatic hotspot joining. Mirrors the Windows app feature set.

The package includes the sharenet binary, the pinned tun2socks engine,
bundled adb for USB transport, a desktop menu entry, an icon, and a
polkit action for elevated launch.

%prep
%autosetup -n %{name}-%{version}

%build
# Binary and engine are pre-built and included in the source tarball
# No compilation happens during RPM build
echo "Using pre-built binaries"

%install
rm -rf %{buildroot}

# Binaries
install -Dm755 dist/sharenet %{buildroot}%{_bindir}/sharenet
install -Dm755 tools/tun2socks %{buildroot}%{_bindir}/tun2socks
install -Dm755 tools/adb %{buildroot}%{_bindir}/adb

# Desktop entry
install -Dm644 packaging/sharenet.desktop %{buildroot}%{_datadir}/applications/sharenet.desktop

# Icon
install -Dm644 ../logo/ShareNet_Logo.svg %{buildroot}%{_datadir}/icons/hicolor/scalable/apps/sharenet.svg

# Polkit action
install -Dm644 packaging/com.sharenet.ui.policy %{buildroot}%{_datadir}/polkit-1/actions/com.sharenet.ui.policy

# Licenses
install -Dm644 packaging/licenses/ShareNet-LICENSE.txt %{buildroot}%{_docdir}/%{name}/ShareNet-LICENSE.txt
install -Dm644 packaging/licenses/tun2socks-LICENSE.txt %{buildroot}%{_docdir}/%{name}/tun2socks-LICENSE.txt

# Documentation
install -Dm644 README.md %{buildroot}%{_docdir}/%{name}/README.md

%post
update-desktop-database %{_datadir}/applications &>/dev/null || :
gtk-update-icon-cache -q -t %{_datadir}/icons/hicolor &>/dev/null || :

%postun
update-desktop-database %{_datadir}/applications &>/dev/null || :
gtk-update-icon-cache -q -t %{_datadir}/icons/hicolor &>/dev/null || :

%files
%{_bindir}/sharenet
%{_bindir}/tun2socks
%{_bindir}/adb
%{_datadir}/applications/sharenet.desktop
%{_datadir}/icons/hicolor/scalable/apps/sharenet.svg
%{_datadir}/polkit-1/actions/com.sharenet.ui.policy
%doc %{_docdir}/%{name}/

%changelog
* Mon Nov 07 2026 Alpheus Mogale <mogalealpheus@users.noreply.github.com> - 0.1.0-1
- Initial RPM release
- Includes sharenet binary, tun2socks engine, bundled adb, desktop entry, icon, polkit action