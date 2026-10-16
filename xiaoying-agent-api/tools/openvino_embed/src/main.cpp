#include <algorithm>
#include <cmath>
#include <cstdint>
#include <filesystem>
#include <iostream>
#include <optional>
#include <sstream>
#include <stdexcept>
#include <string>
#include <vector>

#include <nlohmann/json.hpp>

#include "openvino/genai/tokenizer.hpp"
#include "openvino/openvino.hpp"

namespace {

using json = nlohmann::json;

struct Options {
    std::filesystem::path model_dir;
    std::string device = "CPU";
};

Options parse_args(int argc, char* argv[]) {
    Options options;

    for (int index = 1; index < argc; ++index) {
        const std::string arg = argv[index];
        if (arg == "--model-dir") {
            if (index + 1 >= argc) {
                throw std::runtime_error("Missing value for --model-dir");
            }
            options.model_dir = argv[++index];
            continue;
        }

        if (arg == "--device") {
            if (index + 1 >= argc) {
                throw std::runtime_error("Missing value for --device");
            }
            options.device = argv[++index];
            continue;
        }

        throw std::runtime_error("Unknown argument: " + arg);
    }

    if (options.model_dir.empty()) {
        throw std::runtime_error("Usage: ov-embed --model-dir <MODEL_DIR> [--device CPU]");
    }

    return options;
}

std::string read_stdin() {
    std::ostringstream input;
    input << std::cin.rdbuf();
    return input.str();
}

bool port_has_name(const ov::Output<const ov::Node>& port, const std::string& name) {
    const auto& names = port.get_names();
    if (names.find(name) != names.end()) {
        return true;
    }

    try {
        return port.get_any_name() == name;
    } catch (const std::exception&) {
        return false;
    }
}

std::optional<std::string> find_port_name(const std::vector<ov::Output<const ov::Node>>& ports,
                                          const std::vector<std::string>& preferred_names) {
    for (const auto& candidate : preferred_names) {
        for (const auto& port : ports) {
            if (port_has_name(port, candidate)) {
                return candidate;
            }
        }
    }

    if (!ports.empty()) {
        try {
            return ports.front().get_any_name();
        } catch (const std::exception&) {
        }
    }

    return std::nullopt;
}

float tensor_value_as_float(const ov::Tensor& tensor, std::size_t index) {
    const auto type = tensor.get_element_type();
    if (type == ov::element::f32) {
        return tensor.data<const float>()[index];
    }
    if (type == ov::element::f16) {
        return static_cast<float>(tensor.data<const ov::float16>()[index]);
    }
    if (type == ov::element::bf16) {
        return static_cast<float>(tensor.data<const ov::bfloat16>()[index]);
    }
    if (type == ov::element::i8) {
        return static_cast<float>(tensor.data<const std::int8_t>()[index]);
    }
    if (type == ov::element::u8) {
        return static_cast<float>(tensor.data<const std::uint8_t>()[index]);
    }
    if (type == ov::element::i32) {
        return static_cast<float>(tensor.data<const std::int32_t>()[index]);
    }
    if (type == ov::element::i64) {
        return static_cast<float>(tensor.data<const std::int64_t>()[index]);
    }

    throw std::runtime_error("Unsupported tensor element type: " + type.get_type_name());
}

bool mask_value_is_active(const ov::Tensor& tensor, std::size_t index) {
    const auto type = tensor.get_element_type();
    if (type == ov::element::i64) {
        return tensor.data<const std::int64_t>()[index] != 0;
    }
    if (type == ov::element::i32) {
        return tensor.data<const std::int32_t>()[index] != 0;
    }
    if (type == ov::element::u8) {
        return tensor.data<const std::uint8_t>()[index] != 0;
    }
    if (type == ov::element::boolean) {
        return tensor.data<const bool>()[index];
    }

    return tensor_value_as_float(tensor, index) != 0.0F;
}

void normalize_embedding(std::vector<float>& embedding) {
    float norm = 0.0F;
    for (float value : embedding) {
        norm += value * value;
    }

    norm = std::sqrt(norm);
    if (norm <= 0.0F) {
        return;
    }

    for (float& value : embedding) {
        value /= norm;
    }
}

std::vector<std::vector<float>> tensor_to_embeddings(const ov::Tensor& tensor, bool normalize) {
    const auto shape = tensor.get_shape();
    if (shape.size() != 2) {
        throw std::runtime_error("Expected a 2D embedding tensor");
    }

    const std::size_t batch_size = shape[0];
    const std::size_t dimension = shape[1];
    std::vector<std::vector<float>> embeddings(batch_size, std::vector<float>(dimension, 0.0F));

    for (std::size_t batch = 0; batch < batch_size; ++batch) {
        for (std::size_t dim = 0; dim < dimension; ++dim) {
            embeddings[batch][dim] = tensor_value_as_float(tensor, batch * dimension + dim);
        }
        if (normalize) {
            normalize_embedding(embeddings[batch]);
        }
    }

    return embeddings;
}

std::vector<std::vector<float>> mean_pool_embeddings(const ov::Tensor& hidden_states,
                                                     const ov::Tensor& attention_mask,
                                                     bool normalize) {
    const auto hidden_shape = hidden_states.get_shape();
    const auto mask_shape = attention_mask.get_shape();
    if (hidden_shape.size() != 3 || mask_shape.size() != 2) {
        throw std::runtime_error("Mean pooling expects hidden_states [B,S,H] and attention_mask [B,S]");
    }

    const std::size_t batch_size = hidden_shape[0];
    const std::size_t sequence_length = hidden_shape[1];
    const std::size_t dimension = hidden_shape[2];

    if (mask_shape[0] != batch_size || mask_shape[1] != sequence_length) {
        throw std::runtime_error("attention_mask shape does not match hidden_states");
    }

    std::vector<std::vector<float>> embeddings(batch_size, std::vector<float>(dimension, 0.0F));
    for (std::size_t batch = 0; batch < batch_size; ++batch) {
        float active_tokens = 0.0F;
        for (std::size_t token = 0; token < sequence_length; ++token) {
            if (!mask_value_is_active(attention_mask, batch * sequence_length + token)) {
                continue;
            }

            active_tokens += 1.0F;
            const std::size_t token_offset = (batch * sequence_length + token) * dimension;
            for (std::size_t dim = 0; dim < dimension; ++dim) {
                embeddings[batch][dim] += tensor_value_as_float(hidden_states, token_offset + dim);
            }
        }

        if (active_tokens <= 0.0F) {
            active_tokens = static_cast<float>(sequence_length);
        }

        for (float& value : embeddings[batch]) {
            value /= active_tokens;
        }
        if (normalize) {
            normalize_embedding(embeddings[batch]);
        }
    }

    return embeddings;
}

std::vector<std::vector<float>> infer_embeddings(const Options& options,
                                                 const std::vector<std::string>& texts,
                                                 const json& request) {
    ov::genai::Tokenizer tokenizer(options.model_dir);
    ov::AnyMap tokenization_params;
    if (request.contains("maxLength") && request["maxLength"].is_number_unsigned()) {
        tokenization_params["max_length"] = request["maxLength"].get<std::size_t>();
    }

    ov::Core core;
    auto model = core.read_model(options.model_dir / "openvino_model.xml");
    auto compiled_model = core.compile_model(model, options.device);
    auto infer_request = compiled_model.create_infer_request();

    const auto encoded = tokenizer.encode(texts, tokenization_params);
    infer_request.set_tensor("input_ids", encoded.input_ids);
    infer_request.set_tensor("attention_mask", encoded.attention_mask);

    const auto token_type_ids_name = find_port_name(compiled_model.inputs(), {"token_type_ids"});
    if (token_type_ids_name.has_value()) {
        ov::Tensor token_type_ids(ov::element::i64, encoded.input_ids.get_shape());
        std::fill_n(token_type_ids.data<std::int64_t>(), token_type_ids.get_size(), 0);
        infer_request.set_tensor(*token_type_ids_name, token_type_ids);
    }

    infer_request.infer();

    const auto sentence_embedding_name = find_port_name(compiled_model.outputs(), {"sentence_embedding"});
    if (sentence_embedding_name.has_value() && *sentence_embedding_name == "sentence_embedding") {
        return tensor_to_embeddings(infer_request.get_tensor(*sentence_embedding_name), true);
    }

    const auto hidden_state_name = find_port_name(compiled_model.outputs(), {"last_hidden_state", "token_embeddings"});
    if (!hidden_state_name.has_value()) {
        throw std::runtime_error("Model outputs do not contain sentence_embedding, last_hidden_state, or token_embeddings");
    }

    return mean_pool_embeddings(infer_request.get_tensor(*hidden_state_name), encoded.attention_mask, true);
}

json run_embedding(const Options& options, const json& request) {
    if (!request.contains("texts") || !request["texts"].is_array()) {
        throw std::runtime_error("Request must contain a texts array");
    }

    const std::vector<std::string> texts = request.at("texts").get<std::vector<std::string>>();
    if (texts.empty()) {
        throw std::runtime_error("texts array must not be empty");
    }

    std::vector<std::vector<float>> embeddings = infer_embeddings(options, texts, request);

    const std::size_t dimension = embeddings.empty() ? 0 : embeddings.front().size();
    return json{{"embeddings", embeddings}, {"dimension", dimension}};
}

}  // namespace

int main(int argc, char* argv[]) {
    try {
        const Options options = parse_args(argc, argv);
        const std::string input = read_stdin();
        if (input.empty()) {
            throw std::runtime_error("stdin request is empty");
        }

        const json request = json::parse(input);
        const json response = run_embedding(options, request);
        std::cout << response.dump();
        return 0;
    } catch (const std::exception& ex) {
        const json error_response{{"error", ex.what()}};
        std::cerr << ex.what() << std::endl;
        std::cout << error_response.dump();
        return 1;
    }
}