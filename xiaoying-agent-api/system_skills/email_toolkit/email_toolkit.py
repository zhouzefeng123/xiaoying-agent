"""
@tool email-toolkit
@description 统一邮箱收发工具：支持 SMTP 发信（含附件）与 POP3 收信（自动解码、正文提取、附件检测）
@param action str 必填，'send' 或 'fetch'
@param to str 发信时必填，收件人地址
@param subject str 发信时可选，默认 'No Subject'
@param body str 发信时可选，默认空
@param attachments list 发信时可选，附件路径列表
@param limit int 收信时可选，最多拉取 N 封，默认 1
"""
import os
import json
import sys
import smtplib
from email.mime.text import MIMEText
from email.mime.multipart import MIMEMultipart
from email.mime.base import MIMEBase
from email import encoders
from email.header import decode_header, make_header
import poplib
from email import message_from_bytes
from email.utils import parseaddr

# 强制 UTF-8 输出（Windows 兼容）
sys.stdout.reconfigure(encoding='utf-8')

# 从环境变量读取输入
input_json = os.environ.get('SKILL_INPUT', '{}')
try:
    params = json.loads(input_json)
except json.JSONDecodeError:
    print(json.dumps({'code': 400, 'message': 'Invalid JSON input'}))
    sys.exit(1)

# 读取邮箱配置
config_str = os.environ.get('EMAIL_CONFIG')
config = None

if config_str:
    try:
        config = json.loads(config_str)
    except json.JSONDecodeError:
        print(json.dumps({'code': 400, 'message': 'Invalid EMAIL_CONFIG JSON'}))
        sys.exit(1)
else:
    # Fallback to config.json in the same directory
    script_dir = os.path.dirname(os.path.abspath(__file__))
    config_path = os.path.join(script_dir, 'config.json')
    if os.path.exists(config_path):
        try:
            with open(config_path, 'r', encoding='utf-8') as f:
                config = json.load(f)
        except Exception as e:
            print(json.dumps({'code': 500, 'message': f'Failed to load config.json: {str(e)}'}))
            sys.exit(1)
    else:
        print(json.dumps({'code': 400, 'message': 'EMAIL_CONFIG environment variable not set and config.json not found'}))
        sys.exit(1)

action = params.get('action')

if action == 'send':
    # 发信逻辑
    smtp_server = config.get('smtp_server', 'smtp.163.com')
    smtp_port = config.get('smtp_port', 465)
    sender = config.get('user')
    password = config.get('app_password')
    to = params.get('to')
    subject = params.get('subject', 'No Subject')
    body = params.get('body', '')
    
    # 兼容字符串和列表格式的 attachments 参数
    attachments_param = params.get('attachments', [])
    if isinstance(attachments_param, str):
        # 支持分号或逗号分隔的字符串格式
        attachments = [p.strip() for p in attachments_param.replace(',', ';').split(';') if p.strip()]
    elif isinstance(attachments_param, list):
        attachments = attachments_param
    else:
        attachments = []

    if not all([sender, password, to]):
        print(json.dumps({'code': 400, 'message': 'Missing required fields: user, app_password, or to'}))
        sys.exit(1)

    msg = MIMEMultipart()
    msg['From'] = sender
    msg['To'] = to
    msg['Subject'] = subject
    msg.attach(MIMEText(body, 'plain', 'utf-8'))

    for filepath in attachments:
        if os.path.exists(filepath):
            with open(filepath, 'rb') as f:
                part = MIMEBase('application', 'octet-stream')
                part.set_payload(f.read())
            encoders.encode_base64(part)
            part.add_header(
                'Content-Disposition',
                f'attachment; filename={os.path.basename(filepath)}'
            )
            msg.attach(part)
        else:
            print(json.dumps({'code': 404, 'message': f'Attachment not found: {filepath}'}))
            sys.exit(1)

    try:
        server = smtplib.SMTP_SSL(smtp_server, smtp_port)
        server.login(sender, password)
        server.sendmail(sender, to, msg.as_string())
        server.quit()
        print(json.dumps({'code': 200, 'message': 'Email sent successfully'}))
    except Exception as e:
        print(json.dumps({'code': 500, 'message': f'SMTP error: {str(e)}'}))

elif action == 'fetch':
    # 收信逻辑
    pop_server = config.get('pop_server', 'pop.163.com')
    pop_port = config.get('pop_port', 995)
    user = config.get('user')
    password = config.get('app_password')
    limit = params.get('limit', 1)

    if not all([user, password]):
        print(json.dumps({'code': 400, 'message': 'Missing user or app_password in config'}))
        sys.exit(1)

    try:
        server = poplib.POP3_SSL(pop_server, pop_port)
        server.user(user)
        server.pass_(password)
        num_messages = len(server.list()[1])
        count = min(limit, num_messages)
        emails = []

        # POP3 returns messages in order 1..N (oldest to newest)
        # To fetch LATEST messages, we should fetch from N down to N-count+1
        
        start_index = num_messages
        end_index = max(1, num_messages - count + 1)
        
        # Iterate backwards from newest to oldest requested
        for i in range(start_index, end_index - 1, -1):
            # Get UID for stable identification
            try:
                uid_resp = server.uidl(i)
                # Format: b'+OK 1 UID' or just b'1 UID' depending on implementation/method
                # server.uidl(msg) returns the response line.
                # However, in Python poplib, uidl(i) returns the response string directly.
                # Usually: "+OK 1 000000014..."
                # Wait, poplib.uidl(i) returns string (bytes) response.
                # Let's be safe and split.
                uid_parts = uid_resp.split()
                if len(uid_parts) >= 3 and uid_parts[0] == b'+OK':
                     uid = uid_parts[-1].decode('utf-8') # +OK 1 UID
                elif len(uid_parts) >= 2:
                     uid = uid_parts[-1].decode('utf-8') # 1 UID
                else:
                     uid = str(i)
            except Exception:
                uid = str(i)

            raw_email = b'\n'.join(server.retr(i)[1])
            msg = message_from_bytes(raw_email)
            
            # Decode Headers safely
            subject = str(make_header(decode_header(msg.get('Subject', 'No Subject'))))
            from_header = str(make_header(decode_header(msg.get('From', 'Unknown'))))
            
            # Extract clean address
            realname, email_address = parseaddr(from_header)
            from_display = f"{realname} <{email_address}>" if realname else email_address

            # 提取 text/plain 正文
            body = ''
            if msg.is_multipart():
                for part in msg.walk():
                    if part.get_content_type() == 'text/plain':
                        payload = part.get_payload(decode=True)
                        if payload:
                            charset = part.get_content_charset() or 'utf-8'
                            try:
                                body = payload.decode(charset, errors='ignore')
                            except LookupError:
                                body = payload.decode('utf-8', errors='ignore')
                            break
            else:
                payload = msg.get_payload(decode=True)
                if payload:
                    charset = msg.get_content_charset() or 'utf-8'
                    try:
                        body = payload.decode(charset, errors='ignore')
                    except LookupError:
                        body = payload.decode('utf-8', errors='ignore')
            
            emails.append({
                'id': uid,
                'seq': i,
                'from': from_display,
                'subject': subject,
                'body_preview': body[:500] + '...' if len(body) > 500 else body,
                'timestamp': msg.get('Date', 'Unknown'),
                'message_id': msg.get('Message-ID', '')
            })
        
        server.quit()
        print(json.dumps({'code': 200, 'data': emails}))
    except Exception as e:
        print(json.dumps({'code': 500, 'message': f'POP3 error: {str(e)}'}))
else:
    print(json.dumps({'code': 400, 'message': 'Unsupported action. Use "send" or "fetch"'}))