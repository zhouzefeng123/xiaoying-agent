"""
# Skill Generator

此脚本是 'skill-generator' 技能的核心实现。
该技能的元数据定义（包括参数、描述、使用示例等）完全由同目录下的 `SKILL.md` 文件管理。

**注意**:
- 本文件的任何修改都应同步检查 `SKILL.md` 是否需要更新。
- 系统加载时仅读取 `SKILL.md`，忽略本文件中的任何元数据注释。
"""
import os
import sys
import json
import shutil
import re

# Force UTF-8 output
sys.stdout.reconfigure(encoding='utf-8')

# Define standard skill directory
def get_skill_dir():
    # Assuming this script is in backend/system_skills/skill-generator/
    # We want to return backend/system_skills/
    script_dir = os.path.dirname(os.path.abspath(__file__))
    
    # Check if we are running from 'workspace' (Agent execution context)
    # If so, we need to navigate up to backend/system_skills
    # But usually the agent copies the script or runs it in place.
    # If 'AURAMATE_SKILLS_DIR' env var is set, use it (Best Practice)
    if os.environ.get("AURAMATE_SKILLS_DIR"):
        return os.environ.get("AURAMATE_SKILLS_DIR")
        
    # Fallback for packaged environments where script is in root/system_skills/skill-generator/
    # and we want root/system_skills/
    # If script_dir ends with 'skill-generator', go up one level
    if os.path.basename(script_dir) == "skill-generator":
        return os.path.abspath(os.path.join(script_dir, ".."))
        
    # If script is running directly in system_skills (unlikely but possible in some packaging)
    return script_dir

def list_skills():
    skill_dir = get_skill_dir()
    skills = []
    if os.path.exists(skill_dir):
        for name in os.listdir(skill_dir):
            if os.path.isdir(os.path.join(skill_dir, name)):
                skills.append(name)
    return json.dumps(skills, indent=2)

def delete_skill(skill_name):
    skill_dir = os.path.join(get_skill_dir(), skill_name)
    if os.path.exists(skill_dir):
        try:
            shutil.rmtree(skill_dir)
            return f"Skill '{skill_name}' deleted successfully."
        except Exception as e:
            return f"Error deleting skill '{skill_name}': {str(e)}"
    return f"Skill '{skill_name}' not found."

def escape_yaml_double_quoted(value):
    if value is None:
        return ""
    return str(value).replace("\\", "\\\\").replace('"', '\\"')

def build_parameters_yaml(skill_parameters):
    if not skill_parameters:
        return ""

    params_yaml = "parameters:\n"
    for pname, pdef in skill_parameters.items():
        ptype = pdef.get("type", "string")
        pdesc = escape_yaml_double_quoted(pdef.get("description", ""))
        preq = pdef.get("required", False)

        params_yaml += f"  {pname}:\n"
        params_yaml += f"    type: {ptype}\n"
        params_yaml += f"    description: \"{pdesc}\"\n"
        params_yaml += f"    required: {str(preq).lower()}\n"

    return params_yaml

# Helper to scan code for parameters if not provided
def scan_parameters_from_code(code):
    params = {}
    
    # Simple regex for argparse add_argument
    # Matches: parser.add_argument('--name', help='desc', type=str, required=True)
    # This is a basic heuristic.
    
    lines = code.split('\n')
    for line in lines:
        if 'add_argument' in line or 'click.option' in line:
            # Extract name
            name_match = re.search(r"['\"]--([a-zA-Z0-9_-]+)['\"]", line)
            if name_match:
                name = name_match.group(1)
                
                # Extract help/desc
                desc = f"Parameter {name}"
                help_match = re.search(r"help\s*=\s*['\"]([^'\"]+)['\"]", line)
                if help_match:
                    desc = help_match.group(1)
                
                # Extract type
                typ = "string"
                if "int" in line: typ = "integer"
                if "float" in line: typ = "number"
                if "bool" in line or "store_true" in line or "is_flag=True" in line: typ = "boolean"
                
                # Extract required
                req = False
                if "required=True" in line or "required = True" in line:
                    req = True
                
                params[name] = {
                    "type": typ,
                    "description": desc,
                    "required": req
                }
    return params

if __name__ == "__main__":
    input_str = os.environ.get("SKILL_INPUT", "{}")
    try:
        if not input_str: input_str = "{}"
        params = json.loads(input_str)
        
        skill_name = params.get("skill_name")
        skill_description = params.get("skill_description")
        requirements = params.get("requirements")
        reference_urls = params.get("reference_urls")
        provided_parameters = params.get("parameters") # New optional parameter
        action = params.get("action", "create")
        save_path = params.get("save_path")
        code = params.get("code")
        content = params.get("content")
        
        # Action Dispatch
        if action == "list":
            print(list_skills())
            sys.exit(0)
            
        if action == "delete":
            if not skill_name:
                print("Error: skill_name is required for delete action.")
                sys.exit(1)
            print(delete_skill(skill_name))
            sys.exit(0)

        # Create/Update Action
        if not all([skill_name, skill_description]):
            print("Error: skill_name and skill_description are required for create action.")
            sys.exit(1)

        if not code and not content:
            print("Error: one of 'code' or 'content' is required. Use 'code' for executable skills or 'content' for document-only skills.")
            sys.exit(1)

        is_document_only = not bool(code)

        if is_document_only:
            print(f"Creating document-only skill '{skill_name}'...")
        else:
            print(f"Deploying provided code for skill '{skill_name}'...")
        
        # Determine save path
        # FORCE SECURITY: Always ignore user-provided path and enforce system_skills directory
        # This prevents the agent from writing skills to arbitrary locations
        
        # Default structure: backend/system_skills/{skill_name}/{skill_name_underscore}.py
        skill_dir = os.path.join(get_skill_dir(), skill_name)
        # Normalize filename: replace hyphens with underscores for valid python module name
        filename = skill_name.replace("-", "_") + ".py"
        save_path = os.path.join(skill_dir, filename)
        
        # Create skill metadata file (SKILL.md) if it doesn't exist or update it
        try:
            os.makedirs(skill_dir, exist_ok=True)
            readme_path = os.path.join(skill_dir, "SKILL.md")
            
            # Use provided description, fallback to a simple one if empty
            desc_content = skill_description if skill_description else f"Skill: {skill_name}"
            
            # Infer parameters if not provided
            skill_parameters = provided_parameters
            if not skill_parameters and code:
                print("No parameters provided, scanning code...")
                skill_parameters = scan_parameters_from_code(code)
                print(f"Scanned {len(skill_parameters)} parameters from code.")

            params_yaml = build_parameters_yaml(skill_parameters)

            doc_body = content or f"# {skill_name}\n\n{desc_content}\n"

            frontmatter_lines = [
                "---",
                f"name: {skill_name}",
                f"description: \"{escape_yaml_double_quoted(desc_content)}\"",
            ]

            if is_document_only:
                frontmatter_lines.append("language: markdown")
                frontmatter_lines.append("call_format: document")
            else:
                frontmatter_lines.append("language: python")
                frontmatter_lines.append(f"script_path: {filename}")

            if params_yaml:
                frontmatter_lines.append(params_yaml.rstrip())

            frontmatter_lines.append("---")

            if is_document_only:
                markdown_sections = [doc_body.rstrip()]
            else:
                markdown_sections = [
                    f"# {skill_name}",
                    "",
                    desc_content,
                    "",
                    "## Requirements",
                    requirements or 'No specific requirements',
                    "",
                    "## References",
                    json.dumps(reference_urls, indent=2) if reference_urls else 'None',
                ]

            frontmatter = "\n".join(frontmatter_lines) + "\n\n" + "\n".join(markdown_sections).rstrip() + "\n"
            with open(readme_path, 'w', encoding='utf-8') as f:
                f.write(frontmatter)
            print(f"Generated SKILL.md at: {readme_path}")
                
        except Exception as e:
            print(f"Warning: Failed to create SKILL.md: {e}")

        if not is_document_only and save_path:
            try:
                # Ensure directory exists
                os.makedirs(os.path.dirname(os.path.abspath(save_path)), exist_ok=True)
                with open(save_path, 'w', encoding='utf-8') as f:
                    f.write(code)
                print(f"Skill generated and saved to: {save_path}")
            except Exception as e:
                print(f"Error saving file: {e}")
                print("\nCode Content:\n")
                print(code)
        elif is_document_only:
            print(f"Document-only skill created at: {readme_path}")
            
    except Exception as e:
        print(f"Error executing skill-generator: {e}")
        sys.exit(1)
