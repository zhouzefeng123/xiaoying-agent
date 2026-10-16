import unittest

from xlsx_handler import _extract_action_and_args


class XlsxHandlerCliParsingTests(unittest.TestCase):
    def test_supports_cli_flag_style_read_call(self) -> None:
        action, args, early_result = _extract_action_and_args(
            [
                "xlsx_handler.py",
                "--type",
                "read",
                "--input_path",
                "demo.xlsx",
                "--output_format",
                "json",
            ]
        )

        self.assertIsNone(early_result)
        self.assertEqual(action, "read")
        self.assertEqual(args["input_path"], "demo.xlsx")
        self.assertEqual(args["output_format"], "json")

    def test_treats_schema_probe_as_ready_response(self) -> None:
        action, args, early_result = _extract_action_and_args(
            ["xlsx_handler.py", "--type", "object", "--properties", "{}"]
        )

        self.assertIsNone(action)
        self.assertEqual(args, {})
        self.assertIsNotNone(early_result)
        self.assertEqual(early_result["status"], "ready")

    def test_supports_legacy_positional_json_call(self) -> None:
        action, args, early_result = _extract_action_and_args(
            ["xlsx_handler.py", "read", '{"input_path":"legacy.xlsx","output_format":"json"}']
        )

        self.assertIsNone(early_result)
        self.assertEqual(action, "read")
        self.assertEqual(args["input_path"], "legacy.xlsx")


if __name__ == "__main__":
    unittest.main()