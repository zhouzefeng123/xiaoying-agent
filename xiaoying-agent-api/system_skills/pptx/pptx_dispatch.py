"""
PPTX Toolkit Dispatcher
Unified entry point for PPTX manipulation skills.
"""

import sys
import json
import argparse
from pathlib import Path

# Add scripts directory to path to allow imports
current_dir = Path(__file__).resolve().parent
scripts_dir = current_dir / "scripts"
office_dir = scripts_dir / "office"
sys.path.insert(0, str(scripts_dir))
sys.path.insert(0, str(office_dir))

try:
    from office.unpack import unpack
    from office.pack import pack
    from clean import remove_orphaned_slides, remove_trash_directory, remove_orphaned_rels_files
    from add_slide import create_slide_from_layout, duplicate_slide, parse_source
    from thumbnail import generate_thumbnails
except ImportError as e:
    print(f"Error importing PPTX scripts: {e}", file=sys.stderr)
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


def handle_clean(input_path, output_path, args):
    # output_path is ignored for clean as it modifies in-place (unpacked dir)
    unpacked_dir = Path(input_path)
    if not unpacked_dir.exists() or not unpacked_dir.is_dir():
        raise ValueError(f"Input path must be an unpacked directory: {input_path}")
    
    removed = []
    removed.extend(remove_orphaned_slides(unpacked_dir))
    removed.extend(remove_orphaned_rels_files(unpacked_dir))
    removed.extend(remove_trash_directory(unpacked_dir))
    
    if removed:
        print(f"Cleaned {len(removed)} items:")
        for item in removed:
            print(f"  - {item}")
    else:
        print("Nothing to clean.")


def handle_add_slide(input_path, output_path, args):
    # input_path is unpacked_dir
    source = args.source
    if not source:
        raise ValueError("Argument --source is required for add_slide action")
        
    unpacked_dir = Path(input_path)
    
    source_type, layout_file = parse_source(source)
    
    if source_type == "layout" and layout_file is not None:
        create_slide_from_layout(unpacked_dir, layout_file)
    else:
        duplicate_slide(unpacked_dir, source)


def handle_thumbnail(input_path, output_path, args):
    # output_path is actually output_prefix here
    cols = args.cols
    
    # If output_path is not provided, derive from input filename
    if not output_path:
        output_path = Path(input_path).stem + "-thumbnails"
        
    grid_files = generate_thumbnails(input_path, str(output_path), cols)
    
    print(f"Created {len(grid_files)} grid(s):")
    for grid_file in grid_files:
        print(f"  {grid_file}")


def main():
    parser = argparse.ArgumentParser(description="PPTX Toolkit Dispatcher")
    parser.add_argument("--action", required=True, choices=["unpack", "pack", "clean", "add_slide", "thumbnail"], help="Action to perform")
    parser.add_argument("--input_path", required=True, help="Input file or directory path")
    parser.add_argument("--output_path", help="Output file or directory path")
    
    # Action-specific arguments
    parser.add_argument("--merge_runs", type=lambda x: (str(x).lower() == 'true'), default=True, help="(unpack) Merge adjacent runs")
    parser.add_argument("--simplify_redlines", type=lambda x: (str(x).lower() == 'true'), default=True, help="(unpack) Simplify tracked changes")
    parser.add_argument("--original", help="(pack) Original PPTX file for validation")
    parser.add_argument("--validate", type=lambda x: (str(x).lower() == 'true'), default=True, help="(pack) Validate XML")
    parser.add_argument("--source", help="(add_slide) Source slide or layout")
    parser.add_argument("--cols", type=int, default=3, help="(thumbnail) Number of columns")
    
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
            
        elif args.action == "clean":
            handle_clean(args.input_path, args.output_path, args)
            
        elif args.action == "add_slide":
            handle_add_slide(args.input_path, args.output_path, args)
            
        elif args.action == "thumbnail":
            handle_thumbnail(args.input_path, args.output_path, args)
            
    except Exception as e:
        print(f"Error: {e}", file=sys.stderr)
        sys.exit(1)


if __name__ == "__main__":
    main()
