import importlib.util
from pathlib import Path
import subprocess
import tempfile
import unittest
from unittest.mock import patch

spec = importlib.util.spec_from_file_location("terraform_wrapper", Path(__file__).with_name("terraform.py"))
wrapper = importlib.util.module_from_spec(spec)
spec.loader.exec_module(wrapper)


class TerraformVariablesTest(unittest.TestCase):
    def test_failed_plan_removes_private_remote_variable_copies(self):
        with tempfile.TemporaryDirectory() as root:
            directory = Path(root)
            paths = []

            def run(*args, **kwargs):
                if args[:3] == ("gcloud", "storage", "cp"):
                    Path(args[-1]).write_text("private remote configuration")
                else:
                    paths.extend(Path(arg.split("=", 1)[1]) for arg in args if arg.startswith("-var-file="))
                    self.assertEqual(len(paths), 2)
                    self.assertTrue(all(p.read_text() == "private remote configuration" for p in paths))
                    self.assertTrue(all(p.stat().st_mode & 0o777 == 0o600 for p in paths))
                    self.assertEqual(kwargs["env"]["GOOGLE_OAUTH_ACCESS_TOKEN"], "test-token")
                    self.assertNotIn("test-token", " ".join(args))
                    raise subprocess.CalledProcessError(2, args)

            with patch.object(wrapper, "MODULES", {"app": (directory, ["terraform.tfvars", "shared-vm.auto.tfvars"])}), patch.object(wrapper.sys, "argv", ["terraform.py", "app", "plan"]), patch.dict(wrapper.os.environ, {"GOOGLE_OAUTH_ACCESS_TOKEN": "test-token"}), patch.object(wrapper, "run", side_effect=run):
                with self.assertRaises(subprocess.CalledProcessError):
                    wrapper.main()
            self.assertTrue(all(not p.exists() for p in paths))

    def test_saved_plan_path_is_relative_to_module_and_does_not_fetch_variables(self):
        with tempfile.TemporaryDirectory() as root:
            directory = Path(root)
            (directory / "saved.tfplan").write_bytes(b"saved plan")
            with patch.object(wrapper, "MODULES", {"app": (directory, ["terraform.tfvars"])}), patch.object(wrapper.sys, "argv", ["terraform.py", "app", "apply", "saved.tfplan"]), patch.dict(wrapper.os.environ, {"GOOGLE_OAUTH_ACCESS_TOKEN": "test-token"}), patch.object(wrapper, "run") as run:
                wrapper.main()
            self.assertEqual(run.call_count, 1)
            self.assertEqual(run.call_args.args, ("terraform", f"-chdir={directory}", "apply", "saved.tfplan"))

    def test_failed_fetch_keeps_all_existing_local_files(self):
        with tempfile.TemporaryDirectory() as root:
            directory = Path(root)
            names = ["terraform.tfvars", "shared-vm.auto.tfvars"]
            for name in names:
                (directory / name).write_text("local configuration")

            def run(*args, **kwargs):
                if args[-1].endswith(names[1]):
                    raise subprocess.CalledProcessError(1, args)
                Path(args[-1]).write_text("remote configuration")

            with patch.object(wrapper, "MODULES", {"app": (directory, names)}), patch.object(wrapper.sys, "argv", ["terraform.py", "app", "fetch-vars", "--force"]), patch.object(wrapper, "run", side_effect=run):
                with self.assertRaises(subprocess.CalledProcessError):
                    wrapper.main()
            for name in names:
                self.assertEqual((directory / name).read_text(), "local configuration")
            self.assertEqual(list((directory / ".terraform").iterdir()), [])


if __name__ == "__main__":
    unittest.main()
