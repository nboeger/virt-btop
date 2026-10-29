# Homebrew formula for a tap. To use it:
#   1. Create a repo named nboeger/homebrew-tap and copy this file to Formula/virt-btop.rb
#   2. After each release, update url and sha256 (curl -L <url> | sha256sum)
#   3. Users run: brew install nboeger/tap/virt-btop
class VirtBtop < Formula
  desc "btop-style TUI for libvirt/KVM virtual machines"
  homepage "https://github.com/nboeger/virt-btop"
  url "https://github.com/nboeger/virt-btop/archive/refs/tags/v1.2.0.tar.gz"
  sha256 "PLACEHOLDER"
  license "GPL-3.0-only"

  depends_on "go" => :build
  depends_on "libvirt"

  def install
    system "go", "build", "-tags", "libvirt_dlopen",
           *std_go_args(ldflags: "-s -w -X main.version=#{version}", output: bin/"virt-btop"),
           "./cmd/virt-btop"
  end

  test do
    assert_match version.to_s, shell_output("#{bin}/virt-btop --version")
  end
end
