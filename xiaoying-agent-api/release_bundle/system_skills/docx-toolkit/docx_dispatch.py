"""
DOCX Toolkit Dispatcher
Unified entry point for DOCX manipulation skills.
"""

import sys
import json
import argparse
import tempfile
import zipfile
from pathlib import Path

# Add scripts directory to path to allow imports
current_dir = Path(__file__).resolve().parent
scripts_dir = current_dir / "scripts"
office_dir = scripts_dir / "office"
sys.path.insert(0, str(scripts_dir))
sys.path.insert(0, str(office_dir))

try:
    from unpack import unpack
    from pack import pack
    from office.validators.docx import DocxValidator
    from office.validators import RedliningValidator
except ImportError as e:
    print(f"Error importing DOCX scripts: {e}", file=sys.stderr)
    sys.exit(1)


def handle_unpack(input_path, output_path, args):
    merge_runs = args.merge_runs
    simplify_redlines = args.simplify_redlines
    
    # unpack returns (None, message)
    _, message = unpack(input_path, output_path, merge_runs, simplify_redlines)
    if message.startswith("Error"):
        raise ValueError(message)
    print(message)


def handle_pack(input_path, output_path, args):
    original_file = args.original
    validate = args.validate
    
    # pack returns (None, message)
    _, message = pack(input_path, output_path, original_file, validate)
    if message.startswith("Error"):
        raise ValueError(message)
    print(message)


def handle_validate(input_path, output_path, args):
    """Validate DOCX file or unpacked directory, optionally auto-repairing common issues."""
    input_target = Path(input_path)
    if not input_target.exists():
        raise ValueError(f"Input path does not exist: {input_path}")

    original_file = Path(args.original) if args.original else None
    cleanup_dir = None
    if input_target.is_file() and input_target.suffix.lower() == ".docx":
        cleanup_dir = tempfile.mkdtemp(prefix="docx_dispatch_validate_")
        with zipfile.ZipFile(input_target, "r") as archive:
            archive.extractall(cleanup_dir)
        validation_target = Path(cleanup_dir)
    else:
        validation_target = input_target

    validators = [DocxValidator(str(validation_target), original_file)]
    if original_file:
        validators.append(RedliningValidator(validation_target, original_file, author=args.author))

    try:
        if args.auto_repair:
            total_repairs = sum(validator.repair() for validator in validators)
            if total_repairs:
                print(f"Auto-repaired {total_repairs} issue(s)")

        if all(validator.validate() for validator in validators):
            print("All validations PASSED!")
            return
        raise ValueError("Validation failed: DOCX structure has errors.")
    finally:
        if cleanup_dir:
            import shutil
            shutil.rmtree(cleanup_dir, ignore_errors=True)


def main():
    parser = argparse.ArgumentParser(description="DOCX Toolkit Dispatcher")
    parser.add_argument("--action", required=True, choices=["unpack", "pack", "validate"], help="Action to perform")
    parser.add_argument("--input_path", required=True, help="Input file or directory path")
    parser.add_argument("--output_path", help="Output file or directory path")
    
    # Action-specific arguments
    parser.add_argument("--merge_runs", type=lambda x: (str(x).lower() == 'true'), default=True, help="(unpack) Merge adjacent runs")
    parser.add_argument("--simplify_redlines", type=lambda x: (str(x).lower() == 'true'), default=True, help="(unpack) Simplify tracked changes")
    parser.add_argument("--original", help="(pack) Original DOCX file for validation")
    parser.add_argument("--validate", type=lambda x: (str(x).lower() == 'true'), default=True, help="(pack) Validate XML")
    parser.add_argument("--auto_repair", type=lambda x: (str(x).lower() == 'true'), default=False, help="(validate) Automatically repair common issues")
    parser.add_argument("--author", default="Claude", help="(validate) Author name for redlining validation")
    
    args = parser.parse_args()
            
    try:
        if args.action == "unpack":
            if not args.output_path:
                raise ValueError("output_path is required for unpack action")
            handle_unpack(args.input_path, args.output_path, args)
            
        elif args.action == "pack":
            if not args.output_path:
                raise ValueError("output_path is required for pack action")
            handle_pack(args.input_path, args.output_path, args)
            
        elif args.action == "validate":
            handle_validate(args.input_path, args.output_path, args)
            
    except Exception as e:
        print(f"Error: {e}", file=sys.stderr)
        sys.exit(1)


if __name__ == "__main__":
    main()
