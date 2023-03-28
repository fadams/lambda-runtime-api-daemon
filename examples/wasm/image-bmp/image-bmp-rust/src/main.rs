use std::io::{self, Cursor, Read, Write};
use image::{ImageOutputFormat};

fn main() {
    // Read stdin into a buffer of u8
    let mut buf = Vec::new();
    io::stdin().read_to_end(&mut buf).unwrap();

    // Create an image https://docs.rs/image/latest/image/enum.DynamicImage.html
    // https://docs.rs/image/latest/image/fn.load_from_memory.html#
    let img = image::load_from_memory(&buf).unwrap();

    // Consume the image and return an RGB8 image (8bpp no alpha channel).
    // https://docs.rs/image/latest/image/enum.DynamicImage.html#method.into_rgb8
    let filtered = img.into_rgb8();

    // In image 0.24.6 DynamicImage write_to requires the Seek trait.
    // https://doc.rust-lang.org/std/io/struct.Cursor.html
    let mut buf = Cursor::new(vec![]);
    filtered.write_to(&mut buf, ImageOutputFormat::Bmp).unwrap();

    // Write buffer to stdout
    // into_inner consumes the cursor, returning the underlying value.
    // https://doc.rust-lang.org/std/io/struct.Cursor.html#method.into_inner
    io::stdout().write_all(&buf.into_inner()).unwrap();
    io::stdout().flush().unwrap();
}
