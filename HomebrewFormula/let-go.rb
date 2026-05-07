# typed: false
# frozen_string_literal: true

class LetGo < Formula
  desc "A Clojure dialect implemented as a bytecode VM in Go"
  homepage "https://github.com/nooga/let-go"
  license "MIT"
  version "1.7.2"

  on_macos do
    on_intel do
      url "https://github.com/nooga/let-go/releases/download/v1.7.2/let-go_1.7.2_darwin_amd64.tar.gz"
      sha256 "7939779cbb84a425be7937e1b7a6cdc4b575c4d69a4e42cd038ad622a3a04ed4"
    end
    on_arm do
      url "https://github.com/nooga/let-go/releases/download/v1.7.2/let-go_1.7.2_darwin_arm64.tar.gz"
      sha256 "fd7a0b984d625b85440d10ec724ae0b76fe9530b87d44367d7c99819a1360849"
    end
  end

  on_linux do
    on_intel do
      url "https://github.com/nooga/let-go/releases/download/v1.7.2/let-go_1.7.2_linux_amd64.tar.gz"
      sha256 "07cea14298302510a078783c6d064ede21188bcba43982dfe10bd711fd3d4564"
    end
    on_arm do
      url "https://github.com/nooga/let-go/releases/download/v1.7.2/let-go_1.7.2_linux_arm64.tar.gz"
      sha256 "2d75e915d1139845d7019b18c7afcf247a04260853f7c86d5a24e38bfb40f404"
    end
  end

  def install
    bin.install "lg"
  end

  test do
    assert_equal "2", shell_output("#{bin}/lg -e '(+ 1 1)'").strip
  end
end
