# Declarative systemd-nspawn sandbox for the rlm-agent "factory".
#
# One container, three roles, each with a fixed UID and a dedicated writable
# directory. Roles are separated with plain POSIX permissions only.
#
# SECURITY MODEL: these permissions guard against ACCIDENTAL overwrites by a
# misbehaving agent, not against a hostile one. There is no MAC (AppArmor /
# SELinux) here, and root inside the container can read and write everything.
# Treat this as a data-hygiene boundary, nothing stronger.
{ config, pkgs, lib, ... }:
{
  containers.factory = {
    # Started by the orchestrator on demand, not at boot.
    autoStart = false;

    # Own network namespace without any NAT/forwarding: the sandbox has no
    # internet. Anything that needs to fetch must be pre-seeded in the host
    # store and bind-mounted read-only.
    privateNetwork = true;

    config = { config, pkgs, lib, ... }: {
      # Keep the container from prompting for a release upgrade decision.
      system.stateVersion = "25.05";

      # --- Roles -----------------------------------------------------------
      # Fixed UIDs so the orchestrator can hardcode `runuser -u <role>` and
      # ownership stays stable across rebuilds. Every user gets an equally
      # named group, created with a matching GID.
      users.groups.planner.gid = 2000;
      users.groups.developer.gid = 2001;
      users.groups.tester.gid = 2002;

      users.users.planner = {
        isNormalUser = true;
        uid = 2000;
        group = "planner";
        home = "/home/planner";
      };

      users.users.developer = {
        isNormalUser = true;
        uid = 2001;
        group = "developer";
        home = "/home/developer";
      };

      users.users.tester = {
        isNormalUser = true;
        uid = 2002;
        group = "tester";
        home = "/home/tester";
      };

      # --- Shared workspace -------------------------------------------------
      # Ownership encodes the write contract:
      #   plan/           planner writes, everyone reads  (architecture contract)
      #   knowledge_base/ planner only
      #   code/           developer writes, tester reads   (otherwise nothing to test)
      #   reports/        tester writes
      systemd.tmpfiles.rules = [
        "d /workspace 0755 root root -"
        "d /workspace/plan 0755 planner planner -"
        "d /workspace/knowledge_base 0700 planner planner -"
        "d /workspace/code 0755 developer developer -"
        "d /workspace/reports 0755 tester tester -"
      ];
    };
  };
}
