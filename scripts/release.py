"""Validate immutable release candidates. Uses only the Python standard library."""

import argparse
import json
import os
from pathlib import Path
import re
import subprocess
import urllib.error
import urllib.parse
import urllib.request


IMAGE_REPOSITORY = "hkgdevx/hosted-github-runner"
VERSION_PATTERN = r"v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)"


def validate_version(version):
    if not re.fullmatch(VERSION_PATTERN, version):
        raise ValueError("Expected a vMAJOR.MINOR.PATCH tag without leading zeroes")


def validate_sha(sha):
    if not re.fullmatch(r"[0-9a-f]{40}", sha):
        raise ValueError("Expected a full lowercase commit SHA")


def changelog_entry(content, version):
    """Accept Release Please's linked/unlinked major, minor and patch headings."""
    headings = list(re.finditer(r"^#{1,2} .+$", content, re.MULTILINE))
    pattern = rf"^#{{1,2}} (?:\[{re.escape(version[1:])}\]\([^\r\n]+\)|{re.escape(version[1:])})(?: |$)"
    matching = [i for i, heading in enumerate(headings)
                if re.match(pattern, heading.group())]
    if len(matching) != 1:
        raise ValueError(f"Expected exactly one changelog entry for {version}")
    index = matching[0]
    end = headings[index + 1].start() if index + 1 < len(headings) else len(content)
    entry = content[headings[index].start():end].strip()
    if not entry.partition("\n")[2].strip():
        raise ValueError(f"Empty changelog entry for {version}")
    return entry


def prepare(version, sha, root=Path(".")):
    validate_version(version)
    validate_sha(sha)
    actual = subprocess.check_output(["git", "rev-parse", "HEAD"], cwd=root, text=True).strip()
    if actual != sha:
        raise ValueError("Checkout does not match the resolved release commit")
    manifest = json.loads((root / ".release-please-manifest.json").read_text(encoding="utf-8"))
    if manifest.get(".") != version[1:]:
        raise ValueError("Release manifest version does not match the tag")
    entry = changelog_entry((root / "CHANGELOG.md").read_text(encoding="utf-8"), version)
    instructions = (root / "docs/release-notes.md").read_text(encoding="utf-8").strip()
    (root / "dist").mkdir(exist_ok=True)
    (root / "dist/release-notes.md").write_text(
        f"{entry}\n\n---\n\n{instructions}\n", encoding="utf-8"
    )


def validate_draft(release, version, resolved_sha, expected_sha):
    validate_version(version)
    validate_sha(expected_sha)
    if release.get("tagName") != version or resolved_sha != expected_sha:
        raise ValueError("Draft tag or remote tag commit does not match this candidate")
    if release.get("isDraft") is not True:
        raise ValueError("Release is already published; refusing to change it")
    if release.get("assets") != []:
        raise ValueError("Draft has existing assets (or unknown asset state); inspect it and use a fresh version")


def gh_json(*args):
    return json.loads(subprocess.check_output(["gh", *args], text=True))


def check_draft(version, sha):
    validate_version(version)
    validate_sha(sha)
    repository = os.environ["GITHUB_REPOSITORY"]
    release = gh_json("release", "view", version, "--repo", repository,
                      "--json", "tagName,isDraft,assets")
    remote = gh_json("api", f"repos/{repository}/commits/{version}")
    validate_draft(release, version, remote["sha"], sha)


def check_image(version, opener=urllib.request.urlopen):
    """Fail closed: only an explicit registry MANIFEST_UNKNOWN permits a push."""
    validate_version(version)
    query = urllib.parse.urlencode({
        "service": "registry.docker.io", "scope": f"repository:{IMAGE_REPOSITORY}:pull"
    })
    with opener(f"https://auth.docker.io/token?{query}", timeout=30) as response:
        token = json.load(response)["token"]
    request = urllib.request.Request(
        f"https://registry-1.docker.io/v2/{IMAGE_REPOSITORY}/manifests/{version}",
        headers={
            "Authorization": f"Bearer {token}",
            "Accept": ", ".join([
                "application/vnd.oci.image.index.v1+json",
                "application/vnd.oci.image.manifest.v1+json",
                "application/vnd.docker.distribution.manifest.list.v2+json",
                "application/vnd.docker.distribution.manifest.v2+json",
            ]),
        },
    )
    try:
        with opener(request, timeout=30):
            pass
    except urllib.error.HTTPError as error:
        with error:
            if error.code == 404:
                errors = json.load(error).get("errors", [])
                if errors and all(item.get("code") == "MANIFEST_UNKNOWN" for item in errors):
                    return
            raise ValueError(f"Cannot establish image absence (registry HTTP {error.code}); refusing to push") from None
    raise ValueError(f"Image {IMAGE_REPOSITORY}:{version} already exists; use a fresh version")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    subparsers = parser.add_subparsers(dest="command", required=True)
    for command in ("prepare", "check-draft", "check-image"):
        subparser = subparsers.add_parser(command)
        subparser.add_argument("version")
        if command != "check-image":
            subparser.add_argument("sha")
    args = parser.parse_args()
    try:
        if args.command == "prepare":
            prepare(args.version, args.sha)
        elif args.command == "check-draft":
            check_draft(args.version, args.sha)
        else:
            check_image(args.version)
    except (ValueError, KeyError, OSError, subprocess.CalledProcessError) as error:
        parser.exit(1, f"Release validation failed: {error}\n")


if __name__ == "__main__":
    main()
